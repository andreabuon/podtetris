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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

// ConsolidationPlanReconciler reconciles a ConsolidationPlan object.
// An unexpected Pod CREATE cancels every ConsolidationPlan by deleting it;
// owned PodMoves are garbage-collected via controller owner refs.
type ConsolidationPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=consolidationplans/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Reconcile deletes the ConsolidationPlan when enqueued by an unexpected Pod CREATE.
// Owned PodMoves are removed by Kubernetes garbage collection via controller owner refs.
// Direct ConsolidationPlan watch events are ignored in SetupWithManager so plan creation itself does not immediately tear the plan down.
func (r *ConsolidationPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var plan podtetrisiov1.ConsolidationPlan
	if err := r.Get(ctx, req.NamespacedName, &plan); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Cancelling ConsolidationPlan due to an unexpected Pod creation")
	if err := r.Delete(ctx, &plan); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// mapUnexpectedPodToPlans enqueues all ConsolidationPlans when a Pod CREATE is not an expected replacement for an in-flight move.
func (r *ConsolidationPlanReconciler) mapUnexpectedPodToPlans(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	if isReplacementPod(pod) {
		return nil
	}

	var plans podtetrisiov1.ConsolidationPlanList
	if err := r.List(ctx, &plans); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list ConsolidationPlans for unexpected Pod",
			"pod", client.ObjectKeyFromObject(pod))
		return nil
	}

	var reqs []reconcile.Request
	for i := range plans.Items {
		plan := &plans.Items[i]
		// Ignore pods that already existed when the plan was created (informer resyncs).
		if !pod.CreationTimestamp.After(plan.CreationTimestamp.Time) {
			continue
		}
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(plan)})
	}
	return reqs
}

// isReplacementPod returns whether tje pod was created to replace a pod evicted by the PodMove controller.
func isReplacementPod(pod *corev1.Pod) bool {
	if pod.Labels == nil {
		return false
	}
	// Replacement pods are intercepted by a webhookm which applies the target node name and applies the relative PodMove name as a label.
	_, ok := pod.Labels[podtetrisiov1.PodMoveLabelKey]
	return ok
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConsolidationPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ignoreConsolidationPlanEvents := predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
	onlyPodCreates := predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&podtetrisiov1.ConsolidationPlan{}, builder.WithPredicates(ignoreConsolidationPlanEvents)).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(r.mapUnexpectedPodToPlans),
			builder.WithPredicates(onlyPodCreates),
		).
		Named("consolidationplan").
		Complete(r)
}
