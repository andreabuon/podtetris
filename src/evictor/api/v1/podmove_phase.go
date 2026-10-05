package v1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IsTerminal reports whether the PodMove has finished (Succeeded or Failed).
// Missing conditions are not terminal: DerivePhase maps an empty list to Pending,
// so a newly created PodMove is still considered in progress.
func IsTerminal(conditions []metav1.Condition) bool {
	switch DerivePhase(conditions) {
	case PodMovePhaseFailed, PodMovePhaseSucceeded:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether this PodMove has finished.
func (pm *PodMove) IsTerminal() bool {
	return IsTerminal(pm.Status.Conditions)
}

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
	case meta.IsStatusConditionTrue(conditions, ConditionSourceEvicting):
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
