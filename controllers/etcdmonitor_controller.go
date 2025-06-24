package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	monitoringv1 "github.com/Dev0Pos/etcd-monitor-operator/api/v1"
)

// EtcdMonitorReconciler reconciles an EtcdMonitor object
type EtcdMonitorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=monitoring.example.com,resources=etcdmonitors,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=monitoring.example.com,resources=etcdmonitors/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=monitoring.example.com,resources=etcdmonitors/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *EtcdMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch the EtcdMonitor instance
	etcdMonitor := &monitoringv1.EtcdMonitor{}
	err := r.Get(ctx, req.NamespacedName, etcdMonitor)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("EtcdMonitor resource not found. Ignoring since object was deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get EtcdMonitor")
		return ctrl.Result{}, err
	}

	// Define a fixed interval for this basic version
	const reconcileInterval = 30 * time.Second
	// Define default etcd namespace and labels for auto-discovery
	const defaultEtcdNamespace = "kube-system"
	const defaultEtcdPort = 2379
	// Use a fixed label selector for common etcd pods in kube-system
	etcdLabelSelector := labels.SelectorFromSet(labels.Set{"k8s-app": "etcd"})


	// Calculate next reconciliation time to avoid frequent requeues
	requeueAfter := reconcileInterval - time.Since(etcdMonitor.Status.LastProbeTime.Time)
	if etcdMonitor.Status.LastProbeTime == nil || requeueAfter <= 0 {
		logger.Info("Performing etcd health check", "EtcdMonitor.Name", etcdMonitor.Name)

		// 2. Find etcd Pods based on the fixed selector in the default namespace
		podList := &corev1.PodList{}
		listOpts := []client.ListOption{
			client.InNamespace(defaultEtcdNamespace),
			client.MatchingLabelsSelector{Selector: etcdLabelSelector},
		}

		if err := r.List(ctx, podList, listOpts...); err != nil {
			logger.Error(err, "Failed to list etcd pods", "namespace", defaultEtcdNamespace, "selector", etcdLabelSelector.String())
			r.updateEtcdMonitorStatus(ctx, etcdMonitor, logger, fmt.Sprintf("Failed to list etcd pods in %s with selector %s: %s", defaultEtcdNamespace, etcdLabelSelector.String(), err.Error()))
			return ctrl.Result{RequeueAfter: reconcileInterval}, nil
		}

		if len(podList.Items) == 0 {
			logger.Info("No etcd pods found for fixed selector", "selector", etcdLabelSelector.String(), "namespace", defaultEtcdNamespace)
			r.updateEtcdMonitorStatus(ctx, etcdMonitor, logger, fmt.Sprintf("No etcd pods found matching selector %s in %s", etcdLabelSelector.String(), defaultEtcdNamespace))
			return ctrl.Result{RequeueAfter: reconcileInterval}, nil
		}

		var etcdEndpoints []string
		for _, pod := range podList.Items {
			if pod.Status.PodIP != "" {
				etcdEndpoints = append(etcdEndpoints, fmt.Sprintf("%s:%d", pod.Status.PodIP, defaultEtcdPort))
			}
		}

		if len(etcdEndpoints) == 0 {
			logger.Info("No reachable etcd pod IPs found", "selector", etcdLabelSelector.String(), "namespace", defaultEtcdNamespace)
			r.updateEtcdMonitorStatus(ctx, etcdMonitor, logger, "No reachable etcd pod IPs found")
			return ctrl.Result{RequeueAfter: reconcileInterval}, nil
		}

		logger.Info("Found etcd endpoints", "endpoints", etcdEndpoints)

		// 3. Connect to etcd (without TLS for this minimal version)
		cli, err := clientv3.New(clientv3.Config{
			Endpoints:   etcdEndpoints,
			DialTimeout: 5 * time.Second,
			DialOptions: []grpc.DialOption{grpc.WithBlock()},
		})
		if err != nil {
			logger.Error(err, "Failed to connect to etcd", "Endpoints", etcdEndpoints, "error", err.Error())
			r.updateEtcdMonitorStatus(ctx, etcdMonitor, logger, fmt.Sprintf("Failed to connect to etcd: %s", err.Error()))
			return ctrl.Result{RequeueAfter: reconcileInterval}, nil
		}
		defer cli.Close()

		// 4. Perform health check and status update
		ctxProbe, cancelProbe := context.WithTimeout(ctx, 3*time.Second)
		defer cancelProbe()
		resp, err := cli.Status(ctxProbe, etcdEndpoints[0])
		if err != nil {
			logger.Error(err, "Failed to get etcd status", "Endpoint", etcdEndpoints[0], "error", err.Error())
			etcdMonitor.Status.Healthy = false
			r.updateEtcdMonitorStatus(ctx, etcdMonitor, logger, err.Error())
			return ctrl.Result{RequeueAfter: reconcileInterval}, nil
		}

		etcdMonitor.Status.Healthy = true
		etcdMonitor.Status.Leader = fmt.Sprintf("%x", resp.Leader)
		etcdMonitor.Status.DatabaseSize = resp.DbSize
		etcdMonitor.Status.LastProbeTime = &metav1.Time{Time: time.Now()}

		memberListResp, err := cli.MemberList(ctxProbe)
		if err != nil {
			logger.Error(err, "Failed to get etcd member list")
		} else {
			etcdMonitor.Status.Members = []string{}
			for _, m := range memberListResp.Members {
				etcdMonitor.Status.Members = append(etcdMonitor.Status.Members, m.Name)
			}
		}


		if err := r.Status().Update(ctx, etcdMonitor); err != nil {
			logger.Error(err, "Failed to update EtcdMonitor status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: reconcileInterval}, nil

	} else {
		logger.Info("Skipping etcd health check, will re-check soon", "EtcdMonitor.Name", etcdMonitor.Name, "RequeueAfter", requeueAfter)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
}

// updateEtcdMonitorStatus updates the status of the EtcdMonitor resource
func (r *EtcdMonitorReconciler) updateEtcdMonitorStatus(ctx context.Context, etcdMonitor *monitoringv1.EtcdMonitor, logger logr.Logger, errorMessage string) {
	etcdMonitor.Status.LastProbeTime = &metav1.Time{Time: time.Now()}
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "Unhealthy",
		Message:            errorMessage,
		LastTransitionTime: metav1.Now(),
	}
	if etcdMonitor.Status.Healthy {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Healthy"
		condition.Message = "Etcd cluster is healthy"
	}
	etcdMonitor.Status.Conditions = []metav1.Condition{condition}

	if err := r.Status().Update(ctx, etcdMonitor); err != nil {
		logger.Error(err, "Failed to update EtcdMonitor status during error handling")
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *EtcdMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1.EtcdMonitor{}).
		Complete(r)
}
