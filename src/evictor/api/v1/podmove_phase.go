package v1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DerivePhase maps PodMove conditions onto a single kubectl-friendly phase.
// Conditions remain the source of truth; phase is only a summary.
func DerivePhase(conditions []metav1.Condition) PodMovePhase {
	switch {
	case meta.IsStatusConditionTrue(conditions, ConditionFailed):
		return PodMovePhaseFailed
	case meta.IsStatusConditionTrue(conditions, ConditionReplacementSucceeded):
		return PodMovePhaseSucceeded
	case meta.IsStatusConditionTrue(conditions, ConditionReplacementBound):
		return PodMovePhaseBound
	case meta.IsStatusConditionTrue(conditions, ConditionReplacementClaimed):
		return PodMovePhaseClaimed
	case meta.IsStatusConditionTrue(conditions, ConditionSourceEvicted):
		return PodMovePhaseEvicted
	case meta.IsStatusConditionFalse(conditions, ConditionSourceEvicted):
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
