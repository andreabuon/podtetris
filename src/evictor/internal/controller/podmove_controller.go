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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

// PodMoveReconciler reconciles a PodMove object
type PodMoveReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// ConditionEvicted tracks whether the target pod was successfully evicted.
	ConditionEvicted = "Evicted"
)

// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PodMove object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *PodMoveReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var pm podtetrisiov1.PodMove
	if err := r.Get(ctx, req.NamespacedName, &pm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling PodMove",
		"pod", pm.Spec.Pod.Name,
		"sourceNode", pm.Spec.SourceNode,
		"targetNode", pm.Spec.TargetNode,
	)

	// Already done — keep reconcile idempotent.
	if meta.IsStatusConditionTrue(pm.Status.Conditions, ConditionEvicted) {
		log.Info("Skipping reconciliation because pod eviction has already been performed.")
		return ctrl.Result{}, nil
	}
	if err := r.setCondition(ctx, &pm, ConditionEvicted, metav1.ConditionFalse, "Evicting", "Evicting target pod"); err != nil {
		return ctrl.Result{}, err
	}

	pod, err := r.getPod(ctx, pm.Spec.Pod)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.evictPod(ctx, pod); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.setCondition(ctx, &pm, ConditionEvicted, metav1.ConditionTrue, "Evicted", "Pod eviction has been requested."); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PodMoveReconciler) getPod(ctx context.Context, podRef podtetrisiov1.PodReference) (*corev1.Pod, error) {
	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: podRef.Namespace,
		Name:      podRef.Name,
	}, &pod); err != nil {
		return nil, err
	}

	if pod.UID != podRef.UID {
		return nil, fmt.Errorf("pod UID mismatch: got %s, want %s", pod.UID, podRef.UID)
	}

	return &pod, nil
}

func (r *PodMoveReconciler) evictPod(ctx context.Context, pod *corev1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	return r.SubResource("eviction").Create(ctx, pod, eviction)
}

func (r *PodMoveReconciler) setCondition(
	ctx context.Context,
	pm *podtetrisiov1.PodMove,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) error {
	changed := meta.SetStatusCondition(&pm.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: pm.Generation,
	})
	if !changed {
		return nil
	}
	return r.Status().Update(ctx, pm)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodMoveReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&podtetrisiov1.PodMove{}).
		Named("podmove").
		Complete(r)
}
