package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EtcdMonitorSpec defines the desired state of EtcdMonitor
// In this ultra-minimal version, Spec is empty as the operator
// automatically discovers etcd and requires no user configuration.
type EtcdMonitorSpec struct {
}

// EtcdMonitorStatus defines the observed state of EtcdMonitor
type EtcdMonitorStatus struct {
	// Conditions represents the latest available observations of an object's state
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMapKey:"type"`

	// DatabaseSize is the total size of the etcd database in bytes.
	DatabaseSize int64 `json:"databaseSize"`

	// Healthy indicates if the etcd cluster is currently healthy
	Healthy bool `json:"healthy"`

	// Leader is the ID of the current etcd leader
	Leader string `json:"leader,omitempty"`

	// Members lists the current etcd cluster members
	Members []string `json:"members,omitempty"`

	// LastProbeTime is the last time the etcd cluster was probed
	LastProbeTime *metav1.Time `json:"lastProbeTime,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// EtcdMonitor is the Schema for the etcdmonitors API
type EtcdMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EtcdMonitorSpec   `json:"spec,omitempty"`
	Status EtcdMonitorStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// EtcdMonitorList contains a list of EtcdMonitor
type EtcdMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EtcdMonitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EtcdMonitor{}, &EtcdMonitorList{})
}
