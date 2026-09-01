// Code generated-style deepcopy methods for runtime.Object compatibility.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

func (in *EtherealPod) DeepCopyInto(out *EtherealPod) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.Template.DeepCopyInto(&out.Spec.Template)
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		copy(out.Status.Conditions, in.Status.Conditions)
	}
}

func (in *EtherealPod) DeepCopy() *EtherealPod {
	if in == nil {
		return nil
	}
	out := new(EtherealPod)
	in.DeepCopyInto(out)
	return out
}

func (in *EtherealPod) DeepCopyObject() runtime.Object { return in.DeepCopy() }

func (in *EtherealPodList) DeepCopyInto(out *EtherealPodList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]EtherealPod, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *EtherealPodList) DeepCopy() *EtherealPodList {
	if in == nil {
		return nil
	}
	out := new(EtherealPodList)
	in.DeepCopyInto(out)
	return out
}

func (in *EtherealPodList) DeepCopyObject() runtime.Object { return in.DeepCopy() }
