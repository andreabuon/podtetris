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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// OwnerReference identifies the controller that owns the pod being migrated.
type OwnerReference struct {
	// apiVersion of the owning controller, e.g. "apps/v1".
	// +required
	APIVersion string `json:"apiVersion"`

	// kind of the owning controller, e.g. "Deployment", "StatefulSet", "ReplicaSet", ...
	// +required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// name of the owning controller.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace of the owning controller.
	// +required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// uid of the owning controller at plan time.
	// +required
	// +kubebuilder:validation:MinLength=1
	UID string `json:"uid,omitempty"`
}

// PodReference identifies the specific pod instance targeted for migration,
// pinned to a UID so the eviction controller acts on exactly the pod the
// simulator planned against, not merely "a pod with this name."
type PodReference struct {
	// name is the name of the pod at plan time.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// namespace is the namespace of the pod.
	// +required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// uid is the UID of the pod at plan time.
	// +required
	// +kubebuilder:validation:MinLength=1
	UID string `json:"uid"`
}

// PodMigrationSpec defines the desired state of PodMigration
type PodMigrationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// owner identifies the Resource that owns the pod being migrated
	// +required
	Owner OwnerReference `json:"ownerRef"`

	// podRef identifies the specific pod instance to move across nodes.
	// +required
	PodRef PodReference `json:"podRef"`

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

// PodMigrationStatus defines the observed state of PodMigration.
type PodMigrationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the PodMigration resource.
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

	// +optional
	// +kubebuilder:validation:Enum=Pending;Evicted;Bound;Failed;Expired
	MigrationPhase string `json:"migrationPhase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PodMigration is the Schema for the podmigrations API
type PodMigration struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PodMigration
	// +required
	Spec PodMigrationSpec `json:"spec"`

	// status defines the observed state of PodMigration
	// +optional
	Status PodMigrationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PodMigrationList contains a list of PodMigration
type PodMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PodMigration `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PodMigration{}, &PodMigrationList{})
		return nil
	})
}
