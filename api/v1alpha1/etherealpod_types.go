package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EtherealPodSpec describes the Pod that must keep existing.
type EtherealPodSpec struct {
	Template corev1.PodTemplateSpec `json:"template"`
}

// EtherealPodStatus reports the currently managed Pod state.
type EtherealPodStatus struct {
	PodName    string             `json:"podName,omitempty"`
	Phase      corev1.PodPhase    `json:"phase,omitempty"`
	Restarts   int32              `json:"restarts,omitempty"`
	Ready      bool               `json:"ready,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// EtherealPod ensures that exactly one non-terminal Pod exists for its template.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ep;eps
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Restarts",type="integer",JSONPath=".status.restarts"
type EtherealPod struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EtherealPodSpec   `json:"spec,omitempty"`
	Status EtherealPodStatus `json:"status,omitempty"`
}

// EtherealPodList contains a list of EtherealPod resources.
// +kubebuilder:object:root=true
type EtherealPodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EtherealPod `json:"items"`
}
