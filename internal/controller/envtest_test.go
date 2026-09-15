//go:build envtest

package controller

import (
	"context"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	sundayv1alpha1 "github.com/krav01/homework/api/v1alpha1"
)

func TestEnvtestCRDAdmissionAndStatusSubresource(t *testing.T) {
	crdPath, err := filepath.Abs("../../config/crd/bases")
	if err != nil {
		t.Fatal(err)
	}

	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
	}
	config, err := environment.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := sundayv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubeClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "envtest-system"}}
	if err := kubeClient.Create(ctx, namespace); err != nil {
		t.Fatal(err)
	}

	invalid := &sundayv1alpha1.EtherealPod{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid", Namespace: namespace.Name},
		Spec: sundayv1alpha1.EtherealPodSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{}},
		}},
	}
	if err := kubeClient.Create(ctx, invalid); !apierrors.IsInvalid(err) {
		t.Fatalf("invalid template create error = %v, want Kubernetes Invalid", err)
	}

	valid := &sundayv1alpha1.EtherealPod{
		ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: namespace.Name},
		Spec: sundayv1alpha1.EtherealPodSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example:1"}}},
		}},
	}
	if err := kubeClient.Create(ctx, valid); err != nil {
		t.Fatalf("create valid EtherealPod: %v", err)
	}
	valid.Status.PodName = "managed-pod"
	valid.Status.Ready = true
	if err := kubeClient.Status().Update(ctx, valid); err != nil {
		t.Fatalf("update status subresource: %v", err)
	}

	var got sundayv1alpha1.EtherealPod
	if err := kubeClient.Get(ctx, client.ObjectKeyFromObject(valid), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.PodName != "managed-pod" || !got.Status.Ready {
		t.Fatalf("status = %#v, want persisted status subresource", got.Status)
	}
	if len(got.Spec.Template.Spec.Containers) != 1 || got.Spec.Template.Spec.Containers[0].Image != "example:1" {
		t.Fatalf("spec changed during status update: %#v", got.Spec.Template.Spec)
	}
}
