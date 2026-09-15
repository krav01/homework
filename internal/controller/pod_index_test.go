package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestPodControllerUIDIndex(t *testing.T) {
	t.Parallel()

	controller := true
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{
		UID:        types.UID("owner-123"),
		Controller: &controller,
	}}}}

	got := podControllerUIDIndex(pod)
	if len(got) != 1 || got[0] != "owner-123" {
		t.Fatalf("podControllerUIDIndex() = %#v, want [owner-123]", got)
	}
}

func TestPodControllerUIDIndexWithoutController(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{}
	if got := podControllerUIDIndex(pod); got != nil {
		t.Fatalf("podControllerUIDIndex() = %#v, want nil", got)
	}
}
