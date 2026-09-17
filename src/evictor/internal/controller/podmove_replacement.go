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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

func (r *PodMoveReconciler) reconcileClaimedReplacement(ctx context.Context, pm *podtetrisiov1.PodMove) (ctrl.Result, error) {
	replacement, err := r.findReplacementPod(ctx, pm)
	if err != nil {
		return ctrl.Result{}, err
	}
	if replacementOnTarget(replacement, pm) {
		return r.markReplacementVerified(ctx, pm, replacement)
	}
	return r.reconcileReplacementNotFound(ctx, pm, replacement)
}

// reconcileVerifiedReplacement checks if the verified replacement is Running.
// Each unsuccessful poll lasting runningPollInterval counts as a running attempt;
// after MaxRunningAttempts the PodMove is Failed.
func (r *PodMoveReconciler) reconcileVerifiedReplacement(ctx context.Context, pm *podtetrisiov1.PodMove) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	replacement, err := r.findReplacementPod(ctx, pm)
	if err != nil {
		return ctrl.Result{}, err
	}
	if replacement == nil {
		log.Info("Verified replacement pod not found")
		return ctrl.Result{}, fmt.Errorf("Verified replacement pod not found")
	}
	// PodFailed is treated as success: the PodMove completed even if the replacement later fails for an unrelated reason.
	if replacement.Status.Phase == corev1.PodRunning || replacement.Status.Phase == corev1.PodSucceeded || replacement.Status.Phase == corev1.PodFailed {
		msg := fmt.Sprintf("Replacement pod %s/%s is running", replacement.Namespace, replacement.Name)
		if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionReplacementSucceeded, metav1.ConditionTrue, "Running", msg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	return r.recordFailedRunningAttempt(ctx, pm, replacement)
}

// recordFailedRunningAttempt waits runningPollInterval per attempt. After MaxRunningAttempts
// unsuccessful waits the PodMove is marked Failed; otherwise it requeues for the next poll.
func (r *PodMoveReconciler) recordFailedRunningAttempt(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	waited, err := timeSinceCondition(pm, podtetrisiov1.ConditionReplacementBound, "PodMove Verified time not found")
	if err != nil {
		return ctrl.Result{}, err
	}

	if result, due := waitForNextAttempt(waited, pm.Status.RunningAttempts, runningPollInterval); !due {
		log.Info("Replacement pod is not Running yet",
			"phase", replacement.Status.Phase,
			"waited", waited,
			"runningAttempts", pm.Status.RunningAttempts,
			"maxRunningAttempts", podtetrisiov1.MaxRunningAttempts,
			"requeueAfter", result.RequeueAfter,
		)
		return result, nil
	}

	pm.Status.RunningAttempts++
	attempt := pm.Status.RunningAttempts

	if attempt >= podtetrisiov1.MaxRunningAttempts {
		log.Info("Replacement pod did not reach Running; running attempts exhausted",
			"phase", replacement.Status.Phase,
			"waited", waited,
			"runningAttempts", attempt,
		)
		msg := fmt.Sprintf("Replacement pod %s/%s did not reach Running within %s after %d attempts (last phase %q)",
			replacement.Namespace, replacement.Name, runningPollInterval*time.Duration(attempt), attempt, replacement.Status.Phase)
		if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionReplacementBound, metav1.ConditionTrue, podtetrisiov1.ReasonReplacementNotRunning, msg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.markFailed(ctx, pm, podtetrisiov1.ReasonReplacementNotRunning, msg)
	}

	log.Info("Replacement pod is not Running yet; counting running attempt",
		"phase", replacement.Status.Phase,
		"waited", waited,
		"runningAttempts", attempt,
		"maxRunningAttempts", podtetrisiov1.MaxRunningAttempts,
		"requeueAfter", runningPollInterval,
	)
	if err := r.updateStatus(ctx, pm); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: runningPollInterval}, nil
}

func (r *PodMoveReconciler) markReplacementVerified(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("Verified replacement pod", "pod", replacement.Name, "node", pm.Spec.TargetNode)
	msg := fmt.Sprintf("Replacement pod %s/%s persisted on node %q", replacement.Namespace, replacement.Name, pm.Spec.TargetNode)
	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionReplacementBound, metav1.ConditionTrue, "Verified", msg); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// reconcileReplacementNotFound waits persistPollInterval per attempt for the replacement to
// land on Spec.TargetNode. Once the deadline for the current attempt has passed it records
// a failed persist attempt. TargetNodeInjected is left True until MaxPersistAttempts fails.
func (r *PodMoveReconciler) reconcileReplacementNotFound(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	waited, err := timeSinceCondition(pm, podtetrisiov1.ConditionReplacementClaimed, "PodMove Target injection time not found")
	if err != nil {
		return ctrl.Result{}, err
	}

	if result, due := waitForNextAttempt(waited, pm.Status.PersistAttempts, persistPollInterval); !due {
		log.Info("Waiting for replacement pod to persist on the target node",
			"waited", waited,
			"found", replacement != nil,
			"persistAttempts", pm.Status.PersistAttempts,
			"maxPersistAttempts", podtetrisiov1.MaxPersistAttempts,
			"requeueAfter", result.RequeueAfter,
		)
		return result, nil
	}

	return r.recordFailedPersistAttempt(ctx, pm, replacement, waited)
}

// recordFailedPersistAttempt increments PersistAttempts. After MaxPersistAttempts the PodMove
// is marked Failed; otherwise it requeues for the next poll.
func (r *PodMoveReconciler) recordFailedPersistAttempt(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod, waited time.Duration) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	pm.Status.PersistAttempts++
	attempt := pm.Status.PersistAttempts

	if attempt >= podtetrisiov1.MaxPersistAttempts {
		log.Info("Replacement pod request did not persist; persist attempts exhausted",
			"waited", waited,
			"found", replacement != nil,
			"persistAttempts", attempt,
		)
		msg := fmt.Sprintf("Replacement pod was not found/bound to node %q within %s after %d persist attempts",
			pm.Spec.TargetNode, persistPollInterval*time.Duration(attempt), attempt)
		if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionReplacementClaimed, metav1.ConditionTrue, podtetrisiov1.ReasonReplacementNotPersisted, msg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.markFailed(ctx, pm, podtetrisiov1.ReasonReplacementNotPersisted, msg)
	}

	log.Info("Replacement pod request did not persist; counting persist attempt",
		"waited", waited,
		"found", replacement != nil,
		"persistAttempts", attempt,
		"maxPersistAttempts", podtetrisiov1.MaxPersistAttempts,
		"requeueAfter", persistPollInterval,
	)
	if err := r.updateStatus(ctx, pm); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: persistPollInterval}, nil
}

func (r *PodMoveReconciler) findReplacementPod(ctx context.Context, pm *podtetrisiov1.PodMove) (*corev1.Pod, error) {
	opts := []client.ListOption{
		client.MatchingLabels{podtetrisiov1.PodMoveLabelKey: pm.Name},
	}
	if ns := pm.Spec.Pod.Namespace; ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}

	var list corev1.PodList
	if err := r.List(ctx, &list, opts...); err != nil {
		return nil, err
	}

	var pending *corev1.Pod
	for i := range list.Items {
		pod := &list.Items[i]
		if isOriginalPod(pm, pod) || !pod.DeletionTimestamp.IsZero() {
			continue
		}
		if replacementOnTarget(pod, pm) {
			return pod, nil
		}
		if pending == nil {
			pending = pod
		}
	}
	return pending, nil
}

func replacementOnTarget(pod *corev1.Pod, pm *podtetrisiov1.PodMove) bool {
	if pod == nil || pm == nil {
		return false
	}
	if !pod.DeletionTimestamp.IsZero() || isOriginalPod(pm, pod) {
		return false
	}
	return pod.Spec.NodeName == pm.Spec.TargetNode
}

func isOriginalPod(pm *podtetrisiov1.PodMove, pod *corev1.Pod) bool {
	return pm.Spec.Pod.UID != "" && pod.UID == pm.Spec.Pod.UID
}
