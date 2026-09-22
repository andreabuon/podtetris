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
	"slices"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

// bindImpact describes how a newly bound Pod relates to a ConsolidationPlan.
type bindImpact int

const (
	// bindImpactIrrelevant: the Pod does not touch nodes this plan cares about.
	bindImpactIrrelevant bindImpact = iota
	// bindImpactNodesToFree: the Pod landed on a node the plan intends to empty.
	bindImpactNodesToFree
	// bindImpactInvolved: the Pod landed on a source/target of an active PodMove.
	bindImpactInvolved
)

func (i bindImpact) String() string {
	switch i {
	case bindImpactIrrelevant:
		return "irrelevant"
	case bindImpactNodesToFree:
		return "nodesToFree"
	case bindImpactInvolved:
		return "involved"
	default:
		return "unknown"
	}
}

// podBoundPredicate fires when a Pod acquires Spec.NodeName (scheduler bind or
// create-time pinning). Replacement pods are filtered out here.
func podBoundPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pod, ok := e.Object.(*corev1.Pod)
			return ok && pod.Spec.NodeName != "" && !isReplacementPod(pod)
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPod, okOld := e.ObjectOld.(*corev1.Pod)
			newPod, okNew := e.ObjectNew.(*corev1.Pod)
			if !okOld || !okNew {
				return false
			}
			if isReplacementPod(newPod) {
				return false
			}
			return oldPod.Spec.NodeName == "" && newPod.Spec.NodeName != ""
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// mapBoundPodToPlans enqueues ConsolidationPlans whose remaining moves may be
// compromised by a newly bound Pod.
// Replacement pods and binds that only land on nodesToFree
// or on uninvolved nodes are logged and ignored.
func (r *ConsolidationPlanReconciler) mapBoundPodToPlans(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	if isReplacementPod(pod) {
		return nil
	}
	if pod.Spec.NodeName == "" {
		return nil
	}

	log := logf.FromContext(ctx).WithValues("pod", client.ObjectKeyFromObject(pod), "node", pod.Spec.NodeName)

	var plans podtetrisiov1.ConsolidationPlanList
	if err := r.List(ctx, &plans); err != nil {
		log.Error(err, "Failed to list ConsolidationPlans for bound Pod")
		return nil
	}

	var reqs []reconcile.Request
	for i := range plans.Items {
		plan := &plans.Items[i]
		planKey := client.ObjectKeyFromObject(plan)

		// Plan-only check: avoid listing PodMoves when the bind is on nodesToFree.
		if slices.Contains(plan.Spec.NodesToFree, pod.Spec.NodeName) {
			log.Info("Bound Pod landed on an node that was being freed; requeueing ConsolidationPlan",
				"plan", planKey,
			)
			reqs = append(reqs, reconcile.Request{NamespacedName: planKey})
			continue
		}

		moves, err := listPlanPodMoves(ctx, r.Client, plan)
		if err != nil {
			log.Error(err, "Failed to list PodMoves for ConsolidationPlan", "plan", planKey)
			continue
		}

		impact := classifyBoundPod(plan, pod.Spec.NodeName, moves)
		switch impact {
		case bindImpactIrrelevant:
			log.V(1).Info("Bound Pod does not affect ConsolidationPlan",
				"plan", planKey,
				"impact", impact.String(),
			)
		case bindImpactInvolved:
			log.Info("Bound Pod landed on an involved node; requeueing ConsolidationPlan",
				"plan", planKey,
				"impact", impact.String(),
			)
			reqs = append(reqs, reconcile.Request{NamespacedName: planKey})
		case bindImpactNodesToFree:
			log.Info("Bound Pod landed on an node that was being freed; requeueing ConsolidationPlan",
				"plan", planKey,
				"impact", impact.String(),
			)
			reqs = append(reqs, reconcile.Request{NamespacedName: planKey})
			continue
		}
	}
	return reqs
}

// classifyBoundPod returns how a bind on nodeName affects the plan.
func classifyBoundPod(plan *podtetrisiov1.ConsolidationPlan, nodeName string, moves []podtetrisiov1.PodMove) bindImpact {
	if slices.Contains(plan.Spec.NodesToFree, nodeName) {
		return bindImpactNodesToFree
	}
	if involvedNodes(moves).Has(nodeName) {
		return bindImpactInvolved
	}
	return bindImpactIrrelevant
}

// remainingMovesFit reports whether the plan's unfinished PodMoves still fit
// against live cluster placement after an unexpected bind on an involved node.
//
// The check is not implemented yet: departures first, then pending arrivals,
// against a live snapshot. Until then every plan is treated as still viable.
func (r *ConsolidationPlanReconciler) remainingMovesFit(ctx context.Context, plan *podtetrisiov1.ConsolidationPlan, moves []podtetrisiov1.PodMove) (bool, string) {
	_ = r
	_ = ctx
	_ = plan
	_ = moves
	return true, "live fit check not implemented"
}
