package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sundayv1alpha1 "github.com/codex/sunday-system/api/v1alpha1"
)

func TestEtherealPodReconciler_CreatesManagedPod(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	ep := testEtherealPod()
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(ep).WithObjects(ep).Build()
	reconciler := &EtherealPodReconciler{Client: client, Scheme: scheme}

	if _, err := reconciler.Reconcile(context.Background(), requestFor(ep)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	var pods corev1.PodList
	if err := client.List(context.Background(), &pods); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("managed pod count = %d, want 1", len(pods.Items))
	}
	pod := pods.Items[0]
	if pod.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		t.Fatalf("restart policy = %q, want %q", pod.Spec.RestartPolicy, corev1.RestartPolicyAlways)
	}
	if !metav1.IsControlledBy(&pod, ep) {
		t.Fatal("created Pod is not controlled by EtherealPod")
	}
	var updated sundayv1alpha1.EtherealPod
	if err := client.Get(context.Background(), requestFor(ep).NamespacedName, &updated); err != nil {
		t.Fatalf("Get() EtherealPod error = %v", err)
	}
	if updated.Status.PodName != pod.Name {
		t.Fatalf("status podName = %q, want %q", updated.Status.PodName, pod.Name)
	}
}

func TestEtherealPodReconciler_ReplacesTerminalPod(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	ep := testEtherealPod()
	failed := controlledPod(ep, "failed", corev1.PodFailed)
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(ep).WithObjects(ep, failed).Build()
	reconciler := &EtherealPodReconciler{Client: client, Scheme: scheme}

	if _, err := reconciler.Reconcile(context.Background(), requestFor(ep)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	var pods corev1.PodList
	if err := client.List(context.Background(), &pods); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Status.Phase == corev1.PodFailed {
		t.Fatalf("pods after reconcile = %#v, want one replacement", pods.Items)
	}
}

func TestEtherealPodReconciler_ReplacesDeletedPod(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	ep := testEtherealPod()
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(ep).WithObjects(ep).Build()
	reconciler := &EtherealPodReconciler{Client: client, Scheme: scheme}
	request := requestFor(ep)

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	var firstPods corev1.PodList
	if err := client.List(context.Background(), &firstPods); err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	if len(firstPods.Items) != 1 {
		t.Fatalf("first managed pod count = %d, want 1", len(firstPods.Items))
	}
	firstName := firstPods.Items[0].Name
	if err := client.Delete(context.Background(), &firstPods.Items[0]); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	var replacementPods corev1.PodList
	if err := client.List(context.Background(), &replacementPods); err != nil {
		t.Fatalf("replacement List() error = %v", err)
	}
	if len(replacementPods.Items) != 1 || replacementPods.Items[0].Name == firstName {
		t.Fatalf("replacement pods = %#v, want one new Pod", replacementPods.Items)
	}
}

func TestEtherealPodReconciler_DeletesUnlabelledDuplicate(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	ep := testEtherealPod()
	old := controlledPod(ep, "old", corev1.PodRunning)
	old.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
	newPod := controlledPod(ep, "new", corev1.PodRunning)
	newPod.CreationTimestamp = metav1.Now()
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(ep).WithObjects(ep, old, newPod).Build()
	reconciler := &EtherealPodReconciler{Client: client, Scheme: scheme}

	if _, err := reconciler.Reconcile(context.Background(), requestFor(ep)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	var pods corev1.PodList
	if err := client.List(context.Background(), &pods); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Name != "old" {
		t.Fatalf("pods after reconcile = %#v, want only old", pods.Items)
	}
}

func TestEtherealPodReconciler_UpdatesRestartStatus(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	ep := testEtherealPod()
	pod := controlledPod(ep, "running", corev1.PodRunning)
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{RestartCount: 4}}
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(ep).WithObjects(ep, pod).Build()
	reconciler := &EtherealPodReconciler{Client: client, Scheme: scheme}

	if _, err := reconciler.Reconcile(context.Background(), requestFor(ep)); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	var updated sundayv1alpha1.EtherealPod
	if err := client.Get(context.Background(), requestFor(ep).NamespacedName, &updated); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if updated.Status.Restarts != 4 || !updated.Status.Ready {
		t.Fatalf("status = %#v, want restarts=4 and ready=true", updated.Status)
	}
}

func TestChooseKeeper_PrefersRunningThenOldest(t *testing.T) {
	t.Parallel()

	now := time.Now()
	pending := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pending", CreationTimestamp: metav1.NewTime(now.Add(-time.Hour))}}
	runningNew := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "new", CreationTimestamp: metav1.NewTime(now)}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	runningOld := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "old", CreationTimestamp: metav1.NewTime(now.Add(-time.Minute))}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}

	if got := chooseKeeper([]*corev1.Pod{pending, runningNew, runningOld}); got.Name != "old" {
		t.Fatalf("chooseKeeper() = %q, want old", got.Name)
	}
}

func TestRestartCount(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{Status: corev1.PodStatus{
		InitContainerStatuses: []corev1.ContainerStatus{{RestartCount: 1}},
		ContainerStatuses:     []corev1.ContainerStatus{{RestartCount: 2}, {RestartCount: 3}},
	}}
	if got := restartCount(pod); got != 6 {
		t.Fatalf("restartCount() = %d, want 6", got)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := sundayv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add Sunday scheme: %v", err)
	}
	return scheme
}

func testEtherealPod() *sundayv1alpha1.EtherealPod {
	return &sundayv1alpha1.EtherealPod{
		TypeMeta:   metav1.TypeMeta{APIVersion: sundayv1alpha1.GroupVersion.String(), Kind: "EtherealPod"},
		ObjectMeta: metav1.ObjectMeta{Name: "sunday", Namespace: "default", UID: types.UID("ep-uid")},
		Spec: sundayv1alpha1.EtherealPodSpec{Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "sunday"}},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "sunday-app:dev"}}},
		}},
	}
}

func controlledPod(ep *sundayv1alpha1.EtherealPod, name string, phase corev1.PodPhase) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ep.Namespace, UID: types.UID(name),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: ep.APIVersion, Kind: ep.Kind, Name: ep.Name, UID: ep.UID, Controller: &controller,
			}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func requestFor(ep *sundayv1alpha1.EtherealPod) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ep.Namespace, Name: ep.Name}}
}
