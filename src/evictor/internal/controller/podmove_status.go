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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

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
	return r.updateStatus(ctx, pm)
}

func (r *PodMoveReconciler) updateStatus(ctx context.Context, pm *podtetrisiov1.PodMove) error {
	pm.Status.SyncPhase()
	return r.Status().Update(ctx, pm)
}

func (r *PodMoveReconciler) markFailed(ctx context.Context, pm *podtetrisiov1.PodMove, reason, msg string) error {
	return r.setCondition(ctx, pm, podtetrisiov1.ConditionFailed, metav1.ConditionTrue, reason, msg)
}

func timeSinceCondition(pm *podtetrisiov1.PodMove, condType string) (time.Duration, error) {
	cond := meta.FindStatusCondition(pm.Status.Conditions, condType)
	if cond == nil {
		return 0, fmt.Errorf("PodMove %s condition not found", condType)
	}
	if cond.LastTransitionTime.IsZero() {
		return 0, fmt.Errorf("PodMove %s transition time is zero", condType)
	}
	return time.Since(cond.LastTransitionTime.Time), nil
}

// waitForNextAttempt requeues until the current poll window has elapsed.
// When due is true, the caller should increment the attempt counter.
func waitForNextAttempt(waited time.Duration, attempts int32, interval time.Duration) (result ctrl.Result, due bool) {
	nextDeadline := time.Duration(attempts+1) * interval
	if waited < nextDeadline {
		return ctrl.Result{RequeueAfter: nextDeadline - waited}, false
	}
	return ctrl.Result{}, true
}
