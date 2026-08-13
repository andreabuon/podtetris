/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

const (
	// ConditionEvicted is True after the pod eviction on the source node has been requested.
	ConditionEvicted = "Evicted"
	// ConditionTargetNodeInjected is True after the mutating webhook intercepted the replacement pod CREATE and pinned it to Spec.TargetNode.
	ConditionTargetNodeInjected = "TargetNodeInjected"

	// PodMoveLabelKey is set on replacement pods by the mutating webhook to link them back to the PodMove that claimed the CREATE.
	PodMoveLabelKey = "podtetris.io/podmove"
)

// PodMoveSpec defines the desired state of PodMove
type PodMoveSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// owner identifies the Resource that owns the pod being migrated
	// +required
	Owner metav1.OwnerReference `json:"ownerRef"`

	// podRef identifies the specific pod instance to move across nodes.
	// +required
	Pod corev1.ObjectReference `json:"podRef"`

	// sourceNode is the node the pod currently resides on.
	// +required
	// +kubebuilder:validation:MinLength=1
	SourceNode string `json:"sourceNode"`

	// targetNode is the node the simulator has selected for the replacement pod.
	// The webhook injects this into the recreated pod's spec.nodeName (or nodeAffinity).
	// +required
	// +kubebuilder:validation:MinLength=1
	TargetNode string `json:"targetNode"`
}

// PodMoveStatus defines the observed state of PodMove.
type PodMoveStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the PodMove resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PodMove is the Schema for the podmoves API
type PodMove struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PodMove
	// +required
	Spec PodMoveSpec `json:"spec"`

	// status defines the observed state of PodMove
	// +optional
	Status PodMoveStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PodMoveList contains a list of PodMove
type PodMoveList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PodMove `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PodMove{}, &PodMoveList{})
		return nil
	})
}
