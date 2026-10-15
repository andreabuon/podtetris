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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

// ConsolidationPlanReconciler cancels a ConsolidationPlan when an unexpected Pod
// bind compromises it (on a nodesToFree node, or on a node of a non-terminal PodMove).
// Direct ConsolidationPlan watch events are ignored so plan creation itself does
// not tear the plan down.
type ConsolidationPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile cancels the ConsolidationPlan when an unexpected pod lands on a
// nodesToFree node, or on the source or target node of a non-terminal PodMove.
// Cancelling sets the Cancelled condition and deletes all of the plan's PodMoves.
func (r *ConsolidationPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var plan podtetrisiov1.ConsolidationPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	moves, err := listPlanPodMoves(ctx, r.Client, &plan)
	if err != nil {
		return ctrl.Result{}, err
	}

	// PodMoves may still be created by the planner after cancellation, so keep deleting them.
	if meta.IsStatusConditionTrue(plan.Status.Conditions, podtetrisiov1.ConditionPlanCancelled) {
		return ctrl.Result{}, r.deletePodMoves(ctx, moves)
	}

	toFree := sets.New(plan.Spec.NodesToFree...)
	intruder, err := findUnexpectedPodOnNodes(ctx, r.Client, &plan, toFree.Union(involvedNodes(moves)))
	if err != nil {
		return ctrl.Result{}, err
	}
	if intruder == nil {
		log.Info("ConsolidationPlan remains viable; no unexpected Pod on involved nodes",
			"podMoves", len(moves),
		)
		return ctrl.Result{}, nil
	}

	reason, what := podtetrisiov1.ReasonUnexpectedPodOnInvolvedNode, "node involved in a non-terminal PodMove"
	if toFree.Has(intruder.Spec.NodeName) {
		reason, what = podtetrisiov1.ReasonUnexpectedPodOnNodesToFree, "nodesToFree node"
	}
	log.Info("Cancelling ConsolidationPlan; unexpected Pod on "+what,
		"pod", client.ObjectKeyFromObject(intruder),
		"node", intruder.Spec.NodeName,
		"podMoves", len(moves),
	)

	meta.SetStatusCondition(&plan.Status.Conditions, metav1.Condition{
		Type:               podtetrisiov1.ConditionPlanCancelled,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            fmt.Sprintf("Unexpected pod %s on %s %q", client.ObjectKeyFromObject(intruder), what, intruder.Spec.NodeName),
		ObservedGeneration: plan.Generation,
	})
	if err := r.Status().Update(ctx, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, r.deletePodMoves(ctx, moves)
}

func (r *ConsolidationPlanReconciler) deletePodMoves(ctx context.Context, moves []podtetrisiov1.PodMove) error {
	for i := range moves {
		if err := r.Delete(ctx, &moves[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConsolidationPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&podtetrisiov1.ConsolidationPlan{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc:  func(event.CreateEvent) bool { return false },
			UpdateFunc:  func(event.UpdateEvent) bool { return false },
			DeleteFunc:  func(event.DeleteEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		})).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.mapBoundPodToPlans),
			builder.WithPredicates(podBoundPredicate()),
		).
		Named("consolidationplan").
		Complete(r)
}
