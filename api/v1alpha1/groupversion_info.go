// Package v1alpha1 contains the Sunday System Kubernetes API types.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const Group = "sunday.system"

var (
	GroupVersion  = schema.GroupVersion{Group: Group, Version: "v1alpha1"}
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &EtherealPod{}, &EtherealPodList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
