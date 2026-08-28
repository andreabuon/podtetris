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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ConsolidationPlanSpec defines the desired state of ConsolidationPlan
type ConsolidationPlanSpec struct {
	// freedNodes is how many nodes this plan empties.
	// +required
	// +kubebuilder:validation:Minimum=0
	FreedNodes int `json:"freedNodes"`

	// total cost of all PodMoves in this plan.
	// +required
	// +kubebuilder:validation:Minimum=0
	Cost int `json:"cost"`

	// total score of the plan
	// +required
	Score int `json:"score"`

	// moveCount is the number of PodMoves in the4 plan .
	// +required
	// +kubebuilder:validation:Minimum=0
	MoveCount int `json:"moveCount"`
}

// ConsolidationPlanStatus defines the observed state of ConsolidationPlan.
type ConsolidationPlanStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the ConsolidationPlan resource.
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

// ConsolidationPlan is the Schema for the consolidationplans API
type ConsolidationPlan struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ConsolidationPlan
	// +required
	Spec ConsolidationPlanSpec `json:"spec"`

	// status defines the observed state of ConsolidationPlan
	// +optional
	Status ConsolidationPlanStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ConsolidationPlanList contains a list of ConsolidationPlan
type ConsolidationPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ConsolidationPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ConsolidationPlan{}, &ConsolidationPlanList{})
		return nil
	})
}
