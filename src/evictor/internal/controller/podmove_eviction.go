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
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

func (r *PodMoveReconciler) evictSourcePod(ctx context.Context, pm *podtetrisiov1.PodMove) (ctrl.Result, error) {
	if !meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionSourceEvicting) {
		if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionSourceEvicting, metav1.ConditionTrue, "Evicting", "Evicting source pod"); err != nil {
			return ctrl.Result{}, err
		}
	}

	pod, err := r.getSourcePod(ctx, pm)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return r.sourcePodGoneDuringEviction(ctx, pm)
		}
		return ctrl.Result{}, err
	}

	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	err = r.SubResource("eviction").Create(ctx, pod, eviction)
	if err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return r.sourcePodGoneDuringEviction(ctx, pm)
		case apierrors.IsForbidden(err), apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
			return ctrl.Result{}, r.markFailed(ctx, pm, podtetrisiov1.ReasonEvictionFailed, fmt.Sprintf("Eviction of %s permanently denied: %v", client.ObjectKeyFromObject(pod), err))
		case apierrors.IsTooManyRequests(err):
			return r.requeueEviction(ctx, pm, pod, podtetrisiov1.ReasonBlockedByPDB, err)
		default:
			return r.requeueEviction(ctx, pm, pod, podtetrisiov1.ReasonEvictionFailed, err)
		}
	}

	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionSourceEvicted, metav1.ConditionTrue, "Evicted", "Pod eviction has been requested successfully"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// sourcePodGoneDuringEviction handles a missing source pod while eviction is still outstanding.
// If the webhook already claimed a replacement, treat the missing source as eviction success
// so Claimed+not-Evicted races do not Fail the PodMove.
func (r *PodMoveReconciler) sourcePodGoneDuringEviction(ctx context.Context, pm *podtetrisiov1.PodMove) (ctrl.Result, error) {
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementClaimed) {
		if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionSourceEvicted, metav1.ConditionTrue, "Evicted", "Source pod already gone after replacement was claimed"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, r.markFailed(ctx, pm, podtetrisiov1.ReasonPodNotFound, "Source pod not found during eviction")
}

// requeueEviction records a failed eviction attempt and requeues.
// After MaxEvictionAttempts the PodMove is marked Failed.
func (r *PodMoveReconciler) requeueEviction(ctx context.Context, pm *podtetrisiov1.PodMove, pod *corev1.Pod, reason string, evictionErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	waited, err := timeSinceCondition(pm, podtetrisiov1.ConditionSourceEvicting)
	if err != nil {
		return ctrl.Result{}, err
	}

	if result, due := waitForNextAttempt(waited, pm.Status.EvictionAttempts, evictionRetryInterval); !due {
		log.Info("Eviction failed; waiting before counting attempt",
			"pod", client.ObjectKeyFromObject(pod),
			"reason", reason,
			"waited", waited,
			"evictionAttempts", pm.Status.EvictionAttempts,
			"maxEvictionAttempts", podtetrisiov1.MaxEvictionAttempts,
			"requeueAfter", result.RequeueAfter,
		)
		return result, nil
	}

	pm.Status.EvictionAttempts++
	attempt := pm.Status.EvictionAttempts
	if attempt >= podtetrisiov1.MaxEvictionAttempts {
		msg := fmt.Sprintf("Eviction of %s failed after %d attempts: %v",
			client.ObjectKeyFromObject(pod), attempt, evictionErr)
		if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionSourceEvicting, metav1.ConditionTrue, reason, msg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.markFailed(ctx, pm, reason, msg)
	}

	delay := evictionRetryInterval
	if seconds, ok := apierrors.SuggestsClientDelay(evictionErr); ok && seconds > 0 {
		delay = time.Duration(seconds) * time.Second
	}

	msg := fmt.Sprintf("Eviction failed (attempt %d/%d): %v; will retry",
		attempt, podtetrisiov1.MaxEvictionAttempts, evictionErr)
	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionSourceEvicting, metav1.ConditionTrue, reason, msg); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Eviction failed; will retry",
		"pod", client.ObjectKeyFromObject(pod),
		"reason", reason,
		"evictionAttempts", attempt,
		"maxEvictionAttempts", podtetrisiov1.MaxEvictionAttempts,
		"requeueAfter", delay,
	)
	return ctrl.Result{RequeueAfter: delay}, nil
}

func (r *PodMoveReconciler) getSourcePod(ctx context.Context, pm *podtetrisiov1.PodMove) (*corev1.Pod, error) {
	ref := pm.Spec.Pod
	ns := ref.Namespace
	if ns == "" {
		ns = pm.Namespace
	}

	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &pod); err != nil {
		return nil, err
	}
	if ref.UID != "" && pod.UID != ref.UID {
		return nil, fmt.Errorf("pod UID mismatch: got %s, want %s", pod.UID, ref.UID)
	}
	return &pod, nil
}
