package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTemplateHashIsDeterministic(t *testing.T) {
	t.Parallel()

	template := &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{"z": "1", "a": "2", "m": "3"},
			Annotations: map[string]string{"foo": "bar", "abc": "xyz"},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{"zone": "west", "disk": "ssd"},
			Containers:   []corev1.Container{{Name: "app", Image: "example:latest"}},
		},
	}

	want, err := templateHash(template)
	if err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		got, err := templateHash(template)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("templateHash() = %q, want stable hash %q", got, want)
		}
	}
}
