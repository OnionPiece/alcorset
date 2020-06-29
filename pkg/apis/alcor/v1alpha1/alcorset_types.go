package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AlcorSetSpec defines the desired state of AlcorSet
//  0                                               N=replicas-1
//  |<----------       replicas         -------------->|
//  |                      (partition)                 |
//  |<----  stageReplicas  ---->|                      |
//  +---------------------------+----------------------+
//  | stagePodSpec defined pods | podSpec defined pods |
//  +---------------------------+----------------------+
//  | same common labels, such as app=AlcorSet.Name    |
//  +--------------------------------------------------+
//  | same hostnamePrefix, ordered by 0~N(replicas-1)  |
//  +--------------------------------------------------+
//  |              claimed IPs(fixed)                  |
//  +--------------------------------------------------+
//  after stageReplicas raised to replicas, stagePodSpec will replace current podSpec
//
// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
type AlcorSetSpec struct {
	Replicas int `json:"replicas"`
	// IPs used for Pods if not empty, replicas should be smaller or equal to number of IPs
	IPs []string `json:"ips,omitempty"`
	// if IPs is empty, AlcorSet will try to claim IPs from given IPPool, only valid when OnVPC is false
	IPPool string `json:"ippool,omitempty"`
	// whether AlcorSet is deployed on VPC
	OnVPC bool `json:"onVpc,omitempty"`
	// currently, only SR-IOV scenario supports Mbps
	Mbps           int    `json:"mbps,omitempty"`
	HostnamePrefix string `json:"hostnamePrefix"`
	// whether raise Pod one by one in order
	Sequence        bool                   `json:"sequence,omitempty"`
	PodTemplateSpec corev1.PodTemplateSpec `json:"template"`
}

// AlcorSetStatus defines the observed state of AlcorSet
// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
type AlcorSetStatus struct {
	// Number of pods which are ready
	Count      int      `json:"count"`
	ClaimedIPs []string `json:"claimedIPs"`
	Status     string   `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// AlcorSet is the Schema for the alcorsets API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=alcorsets,scope=Namespaced,shortName=als
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`,priority=0
type AlcorSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlcorSetSpec   `json:"spec,omitempty"`
	Status AlcorSetStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// AlcorSetList contains a list of AlcorSet
type AlcorSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlcorSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlcorSet{}, &AlcorSetList{})
}
