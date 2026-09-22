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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

// listPlanPodMoves returns PodMoves owned by the ConsolidationPlan (controller owner ref).
func listPlanPodMoves(ctx context.Context, c client.Client, plan *podtetrisiov1.ConsolidationPlan) ([]podtetrisiov1.PodMove, error) {
	var list podtetrisiov1.PodMoveList
	if err := c.List(ctx, &list, client.InNamespace(plan.Namespace)); err != nil {
		return nil, err
	}
	owned := make([]podtetrisiov1.PodMove, 0, len(list.Items))
	for i := range list.Items {
		if metav1.IsControlledBy(&list.Items[i], plan) {
			owned = append(owned, list.Items[i])
		}
	}
	return owned, nil
}

// involvedNodes is the union of sourceNode and targetNode across non-terminal PodMoves.
// A PodMove with no conditions yet is not terminal, so its nodes are included.
func involvedNodes(moves []podtetrisiov1.PodMove) sets.Set[string] {
	nodes := sets.New[string]()
	for i := range moves {
		pm := &moves[i]
		if pm.IsTerminal() {
			continue
		}
		if pm.Spec.SourceNode != "" {
			nodes.Insert(pm.Spec.SourceNode)
		}
		if pm.Spec.TargetNode != "" {
			nodes.Insert(pm.Spec.TargetNode)
		}
	}
	return nodes
}
