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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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
// bind compromises it (nodesToFree occupancy, or remaining moves no longer fit).
// Direct ConsolidationPlan watch events are ignored so plan creation itself does
// not tear the plan down.
type ConsolidationPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile aborts the ConsolidationPlan when an unexpected pod occupies a
// nodesToFree node, or when remaining PodMoves no longer fit after a bind on an
// involved node. Owned PodMoves are removed by garbage collection.
func (r *ConsolidationPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var plan podtetrisiov1.ConsolidationPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	intruder, err := findUnexpectedPodOnNodesToFree(ctx, r.Client, &plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	if intruder != nil {
		log.Info("Cancelling ConsolidationPlan; unexpected Pod on nodesToFree",
			"pod", client.ObjectKeyFromObject(intruder),
			"node", intruder.Spec.NodeName,
		)
		if err := r.Delete(ctx, &plan); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	moves, err := listPlanPodMoves(ctx, r.Client, &plan)
	if err != nil {
		return ctrl.Result{}, err
	}

	ok, reason := r.remainingMovesFit(ctx, &plan, moves)
	if ok {
		log.Info("ConsolidationPlan remains viable after bound Pod on involved node",
			"reason", reason,
			"podMoves", len(moves),
		)
		return ctrl.Result{}, nil
	}

	log.Info("Cancelling ConsolidationPlan; remaining PodMoves no longer fit",
		"reason", reason,
		"podMoves", len(moves),
	)
	if err := r.Delete(ctx, &plan); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
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
