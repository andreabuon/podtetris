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
	// verifyRetryInterval is how long to wait between checks that the webhook side
	// effect (TargetNodeInjected) actually persisted a replacement on the target node.
	verifyRetryInterval = 2 * time.Second
	// verifyTimeout is how long to wait after TargetNodeInjected before treating the
	// admission side effect as lost and re-opening the PodMove for a later CREATE.
	verifyTimeout = 30 * time.Second
	// verifyRunningInterval is how long to wait to check whether the verified (persisted) pod is in Running phase
	verifyRunningInterval = 1 * time.Minute
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PodMove object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile
func (r *PodMoveReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	log := logf.FromContext(ctx)

	var pm podtetrisiov1.PodMove
	if err = r.Get(ctx, req.NamespacedName, &pm); err != nil {
		return ctrl.Result{}, err
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

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionPodRunning) {
		log.Info("The PodMove has already been completed (the replacement pod is running on the target node)")
		return ctrl.Result{}, nil
	}

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionPodVerified) {
		replacement, err := r.findReplacementPod(ctx, &pm)
		if err != nil {
			return ctrl.Result{}, err
		}
		if replacement == nil {
			log.Info("The PodMove is verified but the replacement pod cannot be found anymore.")
			return ctrl.Result{}, nil
		}

		if replacement.Status.Phase == corev1.PodRunning {
			msg := fmt.Sprintf("Replacement pod %s/%s is running", replacement.Namespace, replacement.Name)
			if err := r.setCondition(ctx, &pm, podtetrisiov1.ConditionPodRunning, metav1.ConditionTrue, "Running", msg); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		} else {
			log.Info("The replacement pod phase is not 'Running' yet. Checking again in", verifyRunningInterval)
			return ctrl.Result{RequeueAfter: verifyRunningInterval}, nil
		}
	}

	// Admission can mark the PodMove injected but then fail to persist the pod.
	// Verify if the new pod persisted
	replacement, err := r.findReplacementPod(ctx, &pm)
	if err != nil {
		return ctrl.Result{}, err
	}
	if replacementOnTarget(replacement, &pm) {
		log.Info("Verified replacement pod", "pod", replacement.Name, "node", pm.Spec.TargetNode)
		msg := fmt.Sprintf("Replacement pod %s/%s persisted on node %q", replacement.Namespace, replacement.Name, pm.Spec.TargetNode)
		if err := r.setCondition(ctx, &pm, podtetrisiov1.ConditionPodVerified, metav1.ConditionTrue, "Verified", msg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionTargetNodeInjected) {
		return r.requeueOrClearInjection(ctx, &pm, replacement)
	}

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionEvicted) {
		log.Info("Pod eviction has already been performed; currently waiting for the webhook to inject the target node name in a new replacement pod")
		return ctrl.Result{}, nil
	}

	if err := r.setCondition(ctx, &pm, podtetrisiov1.ConditionEvicted, metav1.ConditionFalse, "Evicting", "Evicting target pod"); err != nil {
		return ctrl.Result{}, err
	}

	pod, err := r.getPod(ctx, pm)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.evictPod(ctx, pod); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.setCondition(ctx, &pm, podtetrisiov1.ConditionEvicted, metav1.ConditionTrue, "Evicted", "Pod eviction has been requested."); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PodMoveReconciler) getPod(ctx context.Context, pm podtetrisiov1.PodMove) (*corev1.Pod, error) {
	ref := pm.Spec.Pod
	ns := ref.Namespace
	if ns == "" {
		ns = pm.Namespace
	}

	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      ref.Name,
	}, &pod); err != nil {
		return nil, err
	}

	if ref.UID != "" && pod.UID != ref.UID {
		return nil, fmt.Errorf("pod UID mismatch: got %s, want %s", pod.UID, ref.UID)
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
	if changed {
		pm.Status.SyncPhase()
		return r.Status().Update(ctx, pm)
	}
	return nil
}

func (r *PodMoveReconciler) syncPhase(ctx context.Context, pm *podtetrisiov1.PodMove) error {
	if !pm.Status.SyncPhase() {
		return nil
	}
	return r.Status().Update(ctx, pm)
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

func (r *PodMoveReconciler) requeueOrClearInjection(ctx context.Context, pm *podtetrisiov1.PodMove, replacement *corev1.Pod) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	injected := meta.FindStatusCondition(pm.Status.Conditions, podtetrisiov1.ConditionTargetNodeInjected)
	waited := time.Duration(0)
	if injected != nil && !injected.LastTransitionTime.IsZero() {
		waited = time.Since(injected.LastTransitionTime.Time)
	}
	if waited < verifyTimeout {
		log.V(1).Info("Waiting for replacement pod to persist on the target node",
			"waited", waited,
			"found", replacement != nil,
		)
		return ctrl.Result{RequeueAfter: verifyRetryInterval}, nil
	}

	log.Info("Replacement pod did not persist on the target node; clearing TargetNodeInjected",
		"waited", waited,
		"found", replacement != nil,
	)
	msg := fmt.Sprintf("Replacement pod was not found bound to node %q within %s; TargetNodeInjected cleared so a later CREATE can be claimed", pm.Spec.TargetNode, verifyTimeout)
	if err := r.setCondition(ctx, pm, podtetrisiov1.ConditionTargetNodeInjected, metav1.ConditionFalse, "ReplacementNotPersisted", msg); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: verifyRetryInterval}, nil
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

// SetupWithManager sets up the controller with the Manager.
func (r *PodMoveReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&podtetrisiov1.PodMove{}).
		Named("podmove").
		Complete(r)
}
