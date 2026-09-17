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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
)

const (
	// evictionRetryInterval is how often to wait to try eviction again.
	evictionRetryInterval = 2 * time.Minute
	// persistPollInterval is how long to wait between checks that a webhook-claimed replacement persisted on the target node.
	persistPollInterval = 25 * time.Second
	// runningPollInterval is how long to wait between checks that a verified replacement has reached Running.
	runningPollInterval = 3 * time.Minute
)

// PodMoveReconciler reconciles a PodMove object.
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
		if syncErr := r.updateStatus(ctx, &pm); syncErr != nil && err == nil {
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

	// Eviction runs before Claimed/Bound so a webhook that claimed first still gets
	// SourceEvicted set (and eviction retried) until the eviction API succeeds.
	if !meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionSourceEvicted) {
		return r.evictSourcePod(ctx, &pm)
	}

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementSucceeded) {
		log.Info("PodMove already succeeded")
		return ctrl.Result{}, nil
	}

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementBound) {
		return r.reconcileVerifiedReplacement(ctx, &pm)
	}

	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionReplacementClaimed) {
		return r.reconcileClaimedReplacement(ctx, &pm)
	}

	log.Info("Waiting for webhook to claim a replacement pod CREATE")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodMoveReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		For(&podtetrisiov1.PodMove{}).
		Named("podmove").
		Complete(r)
}
