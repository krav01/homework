// Package controller reconciles EtherealPod resources with Kubernetes Pods.
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sundayv1alpha1 "github.com/krav01/homework/api/v1alpha1"
)

const (
	ownerUIDLabel          = "sunday.system/etherealpod-uid"
	ownerNameLabel         = "sunday.system/etherealpod"
	templateHashAnnotation = "sunday.system/template-hash"
	ownerNameAnnotation    = "sunday.system/etherealpod-name"
)

// EtherealPodReconciler keeps one active Pod for every EtherealPod resource.
type EtherealPodReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile converges the managed Pod set to one non-terminal Pod.
func (r *EtherealPodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ep sundayv1alpha1.EtherealPod
	if err := r.Get(ctx, req.NamespacedName, &ep); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !ep.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	pods, err := r.managedPods(ctx, &ep)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("list managed pods: %w", err)
	}

	active, terminal := partitionPods(pods)
	for i := range terminal {
		if !terminal[i].DeletionTimestamp.IsZero() {
			continue
		}
		if err := r.Delete(ctx, terminal[i]); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete terminal pod %s: %w", terminal[i].Name, err)
		}
	}

	// A terminating Pod may still be draining requests or holding its writer lock.
	// Do not overlap it with a replacement, even on a ReadWriteOnce volume.
	if len(terminal) > 0 {
		return ctrl.Result{RequeueAfter: time.Second}, r.updateStatusReason(ctx, &ep, terminal[0], "PodsTerminating")
	}

	if len(active) == 0 {
		pod, err := r.newPod(&ep)
		if err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, pod); err != nil {
			return ctrl.Result{}, fmt.Errorf("create managed pod: %w", err)
		}
		logger.Info("created managed pod", "etherealPod", ep.Name, "pod", pod.Name)
		if err := r.updateStatus(ctx, &ep, pod); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	keeper := chooseKeeper(active)
	for i := range active {
		if active[i].UID == keeper.UID {
			continue
		}
		if err := r.Delete(ctx, active[i]); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete duplicate pod %s: %w", active[i].Name, err)
		}
	}

	if len(active) > 1 {
		return ctrl.Result{RequeueAfter: time.Second}, r.updateStatusReason(ctx, &ep, keeper, "RemovingDuplicates")
	}
	desiredHash, err := templateHash(&ep.Spec.Template)
	if err != nil {
		return ctrl.Result{}, err
	}
	if keeper.Annotations[templateHashAnnotation] != desiredHash {
		if err := r.Delete(ctx, keeper); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("delete outdated pod: %w", err)
		}
		return ctrl.Result{RequeueAfter: time.Second}, r.updateStatusReason(ctx, &ep, keeper, "TemplateChanged")
	}

	if err := r.updateStatus(ctx, &ep, keeper); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *EtherealPodReconciler) managedPods(ctx context.Context, ep *sundayv1alpha1.EtherealPod) ([]*corev1.Pod, error) {
	var list corev1.PodList
	if err := r.List(ctx, &list, client.InNamespace(ep.Namespace)); err != nil {
		return nil, err
	}

	pods := make([]*corev1.Pod, 0, len(list.Items))
	for i := range list.Items {
		if metav1.IsControlledBy(&list.Items[i], ep) {
			pods = append(pods, &list.Items[i])
		}
	}
	return pods, nil
}

func (r *EtherealPodReconciler) newPod(ep *sundayv1alpha1.EtherealPod) (*corev1.Pod, error) {
	template := ep.Spec.Template.DeepCopy()
	labels := make(map[string]string, len(template.Labels)+2)
	for key, value := range template.Labels {
		labels[key] = value
	}
	labels[ownerUIDLabel] = string(ep.UID)
	labels[ownerNameLabel] = ep.Name
	if len(ep.Name) > 63 {
		labels[ownerNameLabel] = fmt.Sprintf("ep-%x", sha256.Sum256([]byte(ep.Name)))[:63]
	}
	annotations := cloneMap(template.Annotations)
	if annotations == nil {
		annotations = make(map[string]string)
	}
	hash, err := templateHash(template)
	if err != nil {
		return nil, err
	}
	annotations[templateHashAnnotation] = hash
	annotations[ownerNameAnnotation] = ep.Name
	prefix := ep.Name
	if len(prefix) > 52 {
		prefix = prefix[:52]
	}
	prefix = strings.TrimRight(prefix, "-.") + "-"

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix,
			Namespace:    ep.Namespace,
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: *template.Spec.DeepCopy(),
	}
	pod.Spec.RestartPolicy = corev1.RestartPolicyAlways
	if err := controllerutil.SetControllerReference(ep, pod, r.Scheme); err != nil {
		return nil, fmt.Errorf("set pod owner reference: %w", err)
	}
	return pod, nil
}

func templateHash(template *corev1.PodTemplateSpec) (string, error) {
	encoded, err := json.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("encode pod template: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func (r *EtherealPodReconciler) updateStatus(ctx context.Context, ep *sundayv1alpha1.EtherealPod, pod *corev1.Pod) error {
	return r.updateStatusReason(ctx, ep, pod, "")
}

func (r *EtherealPodReconciler) updateStatusReason(ctx context.Context, ep *sundayv1alpha1.EtherealPod, pod *corev1.Pod, reason string) error {
	previous := ep.DeepCopy().Status
	ready := podReady(pod) && pod.DeletionTimestamp.IsZero() && reason == ""
	if reason == "" {
		reason = readyReason(ready)
	}
	ep.Status.PodName = pod.Name
	ep.Status.Phase = pod.Status.Phase
	ep.Status.Restarts = restartCount(pod)
	ep.Status.Ready = ready
	apimeta.SetStatusCondition(&ep.Status.Conditions, metav1.Condition{
		Type: "Ready", Status: conditionStatus(ready), ObservedGeneration: ep.Generation,
		Reason: reason, Message: fmt.Sprintf("managed pod %s: %s", pod.Name, reason),
	})
	if reflect.DeepEqual(previous, ep.Status) {
		return nil
	}
	if err := r.Status().Update(ctx, ep); err != nil {
		return fmt.Errorf("update EtherealPod status: %w", err)
	}
	return nil
}

// SetupWithManager registers watches for EtherealPods and their owned Pods.
func (r *EtherealPodReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).
		For(&sundayv1alpha1.EtherealPod{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func partitionPods(pods []*corev1.Pod) (active, terminal []*corev1.Pod) {
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			terminal = append(terminal, pod)
			continue
		}
		active = append(active, pod)
	}
	return active, terminal
}

func chooseKeeper(pods []*corev1.Pod) *corev1.Pod {
	sort.Slice(pods, func(i, j int) bool {
		iRunning := pods[i].Status.Phase == corev1.PodRunning
		jRunning := pods[j].Status.Phase == corev1.PodRunning
		if iRunning != jRunning {
			return iRunning
		}
		if !pods[i].CreationTimestamp.Equal(&pods[j].CreationTimestamp) {
			return pods[i].CreationTimestamp.Before(&pods[j].CreationTimestamp)
		}
		return pods[i].Name < pods[j].Name
	})
	return pods[0]
}

func restartCount(pod *corev1.Pod) int32 {
	var total int32
	for _, status := range pod.Status.InitContainerStatuses {
		total += status.RestartCount
	}
	for _, status := range pod.Status.ContainerStatuses {
		total += status.RestartCount
	}
	return total
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func conditionStatus(ready bool) metav1.ConditionStatus {
	if ready {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func readyReason(ready bool) string {
	if ready {
		return "PodReady"
	}
	return "PodNotReady"
}
