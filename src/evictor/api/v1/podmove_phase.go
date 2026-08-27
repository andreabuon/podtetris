package v1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DerivePhase maps PodMove conditions onto a single kubectl-friendly phase.
// Conditions remain the source of truth; phase is only a summary.
func DerivePhase(conditions []metav1.Condition) PodMovePhase {
	switch {
	case meta.IsStatusConditionTrue(conditions, ConditionPodRunning):
		return PodMovePhaseSucceeded
	case meta.IsStatusConditionTrue(conditions, ConditionPodVerified):
		return PodMovePhaseVerified
	case meta.IsStatusConditionTrue(conditions, ConditionFailed):
		return PodMovePhaseFailed
	case meta.IsStatusConditionTrue(conditions, ConditionTargetNodeInjected):
		return PodMovePhaseVerifying
	case meta.IsStatusConditionTrue(conditions, ConditionEvicted):
		return PodMovePhaseEvicted
	case meta.IsStatusConditionFalse(conditions, ConditionEvicted):
		return PodMovePhaseEvicting
	default:
		return PodMovePhasePending
	}
}

// SyncPhase overwrites Phase from Conditions. It returns true when Phase changed.
func (s *PodMoveStatus) SyncPhase() bool {
	phase := DerivePhase(s.Conditions)
	if s.Phase == phase {
		return false
	}
	s.Phase = phase
	return true
}
