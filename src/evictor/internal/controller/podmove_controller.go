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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

const (
	// persistPollInterval is how often to re-check that a webhook-claimed replacement actually persisted on the target node.
	persistPollInterval = 25 * time.Second
	// persistBindTimeout is how long to wait for a labeled replacement pod to bind to Spec.TargetNode before counting a failed persist attempt.
	persistBindTimeout = 1 * time.Minute
	// runningPollInterval is how long to wait between checks that a verified replacement has reached Running.
	runningPollInterval = 3 * time.Minute
)

// PodMoveReconciler reconciles a PodMove object
type PodMoveReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=podtetris.io.podtetris.io,resources=podmoves/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/eviction,verbs=create

func (r *PodMoveReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)

	var pm podtetrisiov1.PodMove
	if err = r.Get(ctx, req.NamespacedName, &pm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	defer func() {
		if syncErr := r.syncPhase(ctx, &pm); syncErr != nil && err == nil {
			err = syncErr
		}
	}()

	log.Info("Reconciling PodMove",
		"pod", pm.Spec.Pod.Name,
		"sourceNode", pm.Spec.SourceNode,
		"targetNode", pm.Spec.TargetNode,
	)

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionFailed) {
		log.Info("PodMove already failed")
		return ctrl.Result{}, nil
	}
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionPodRunning) {
		log.Info("PodMove already succeeded")
		return ctrl.Result{}, nil
	}
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionPodVerified) {
		return r.reconcileVerifiedReplacement(ctx, &pm)
	}

	// PodMove not Verified yet
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionTargetNodeInjected) {
		replacement, err := r.findReplacementPod(ctx, &pm)
		if err != nil {
			return ctrl.Result{}, err
		}
		if replacementOnTarget(replacement, &pm) {
			return r.markReplacementVerified(ctx, &pm, replacement)
		}
		return r.reconcileUnpersistedReplacement(ctx, &pm, replacement)
	}

	// PodMove not Injected yet
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionEvicted) {
		log.Info("Waiting for webhook to claim a replacement pod CREATE")
		return ctrl.Result{}, nil
	}

	// PodMove not Evicted yet
	return r.evictSourcePod(ctx, &pm)
}

// reconcileVerifiedReplacement records TargetPodRunning if the verified replacement is Running, otherwise requeues.
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
	if replacement.Status.Phase != corev1.PodRunning {
		log.Info("Replacement pod is not Running yet", "phase", replacement.Status.Phase, "requeueAfter", runningPollInterval)
		return ctrl.Result{RequeueAfter: runningPollInterval}, nil
	}

	msg := fmt.Sprintf("Replacement pod %s/%s is running", replacement.Namespace, replacement.Name)
	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionPodRunning, metav1.ConditionTrue, "Running", msg); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *PodMoveReconciler) markReplacementVerified(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod) (ctrl.Result, error) {
	logf.FromContext(ctx).Info("Verified replacement pod", "pod", replacement.Name, "node", pm.Spec.TargetNode)
	msg := fmt.Sprintf("Replacement pod %s/%s persisted on node %q", replacement.Namespace, replacement.Name, pm.Spec.TargetNode)
	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionPodVerified, metav1.ConditionTrue, "Verified", msg); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// reconcileUnpersistedReplacement handles TargetNodeInjected=True when the claimed pod is not yet on Spec.TargetNode.
// It either waits for persistence, reopens the PodMove for another CREATE, or marks Failed.
func (r *PodMoveReconciler) reconcileUnpersistedReplacement(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	waited := timeSinceInjected(pm)

	if stillWaitingForReplacement(waited, replacement) {
		log.V(1).Info("Waiting for replacement pod to persist on the target node",
			"waited", waited,
			"found", replacement != nil,
			"persistAttempts", pm.Status.PersistAttempts,
		)
		return ctrl.Result{RequeueAfter: persistPollInterval}, nil
	}

	return r.recordFailedPersistAttempt(ctx, pm, replacement, waited)
}

// stillWaitingForReplacement reports whether this reconcile should keep waiting rather than
// counting a failed persist attempt.
// A CREATE that never produces a labeled pod is treated as lost after persistPollInterval so the ReplicaSet's next CREATE can re-claim the PodMove.
// A labeled pod that has not bound yet is given persistBindTimeout to schedule onto Spec.TargetNode.
func stillWaitingForReplacement(waited time.Duration, replacement *corev1.Pod) bool {
	if replacement == nil {
		return waited < persistPollInterval
	}
	return waited < persistBindTimeout
}

func timeSinceInjected(pm *podtetrisiov1.PodMove) time.Duration {
	injected := meta.FindStatusCondition(pm.Status.Conditions, podtetrisiov1.ConditionTargetNodeInjected)
	if injected == nil || injected.LastTransitionTime.IsZero() {
		return 0
	}
	return time.Since(injected.LastTransitionTime.Time)
}

// recordFailedPersistAttempt increments PersistAttempts. After MaxPersistAttempts the PodMove
// is marked Failed; otherwise TargetNodeInjected is cleared so a later CREATE can claim it.
func (r *PodMoveReconciler) recordFailedPersistAttempt(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod, waited time.Duration) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	pm.Status.PersistAttempts++
	attempt := pm.Status.PersistAttempts

	if attempt >= podtetrisiov1.MaxPersistAttempts {
		log.Info("Replacement pod did not persist; persist attempts exhausted",
			"waited", waited,
			"found", replacement != nil,
			"persistAttempts", attempt,
		)
		return ctrl.Result{}, r.markReplacementNotPersisted(ctx, pm, attempt)
	}

	log.Info("Replacement pod did not persist; reopening PodMove for another CREATE",
		"waited", waited,
		"found", replacement != nil,
		"persistAttempts", attempt,
	)
	return ctrl.Result{}, r.reopenForReplacementClaim(ctx, pm, attempt)
}

func (r *PodMoveReconciler) markReplacementNotPersisted(ctx context.Context, pm *podtetrisiov1.PodMove, attempt int32) error {
	msg := fmt.Sprintf("Replacement pod was not found bound to node %q within %s after %d persist attempts", pm.Spec.TargetNode, persistBindTimeout, attempt)
	meta.SetStatusCondition(&pm.Status.Conditions, metav1.Condition{
		Type:               podtetrisiov1.ConditionFailed,
		Status:             metav1.ConditionTrue,
		Reason:             podtetrisiov1.ReasonReplacementNotPersisted,
		Message:            msg,
		ObservedGeneration: pm.Generation,
	})
	return r.updateStatus(ctx, pm)
}

func (r *PodMoveReconciler) reopenForReplacementClaim(ctx context.Context, pm *podtetrisiov1.PodMove, attempt int32) error {
	msg := fmt.Sprintf("Replacement pod was not found bound to node %q within %s (attempt %d/%d); TargetNodeInjected cleared so a later CREATE can be claimed", pm.Spec.TargetNode, persistBindTimeout, attempt, podtetrisiov1.MaxPersistAttempts)
	meta.SetStatusCondition(&pm.Status.Conditions, metav1.Condition{
		Type:               podtetrisiov1.ConditionTargetNodeInjected,
		Status:             metav1.ConditionFalse,
		Reason:             podtetrisiov1.ReasonReplacementNotPersisted,
		Message:            msg,
		ObservedGeneration: pm.Generation,
	})
	return r.updateStatus(ctx, pm)
}

func (r *PodMoveReconciler) evictSourcePod(ctx context.Context, pm *podtetrisiov1.PodMove) (ctrl.Result, error) {
	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionEvicted, metav1.ConditionFalse, "Evicting", "Evicting target pod"); err != nil {
		return ctrl.Result{}, err
	}

	pod, err := r.getSourcePod(ctx, pm)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.requestEviction(ctx, pod); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionEvicted, metav1.ConditionTrue, "Evicted", "Pod eviction has been requested"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
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

func (r *PodMoveReconciler) requestEviction(ctx context.Context, pod *corev1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}
	return r.SubResource("eviction").Create(ctx, pod, eviction)
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
	if pod.Spec.NodeSelector[podtetrisiov1.TargetNodeSelectorKey] != pm.Spec.TargetNode {
		return false
	}
	return pod.Spec.NodeName == pm.Spec.TargetNode
}

func isOriginalPod(pm *podtetrisiov1.PodMove, pod *corev1.Pod) bool {
	return pm.Spec.Pod.UID != "" && pod.UID == pm.Spec.Pod.UID
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
	return r.updateStatus(ctx, pm)
}

func (r *PodMoveReconciler) updateStatus(ctx context.Context, pm *podtetrisiov1.PodMove) error {
	pm.Status.SyncPhase()
	return r.Status().Update(ctx, pm)
}

func (r *PodMoveReconciler) syncPhase(ctx context.Context, pm *podtetrisiov1.PodMove) error {
	if !pm.Status.SyncPhase() {
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
