package main

import (
	"time"

	"go.uber.org/zap"
)

type RunOutcome string

const (
	OutcomeNone        RunOutcome = "none"
	OutcomeNotWorth    RunOutcome = "not-worth"
	OutcomeDryRun      RunOutcome = "dry-run"
	OutcomeApplied     RunOutcome = "applied"
	OutcomeApplyFailed RunOutcome = "apply-failed"
)

// RunSummary collects the figures of a single planner run.
// It is logged once, as the last line, with the message "Run summary".
type RunSummary struct {
	Start time.Time

	WorkerNodes    int
	Pods           int
	CandidateNodes int
	CandidateSets  int

	EvictedPods               int
	Permutations              int
	SimulationsFailed         int
	SimulationsWithFreedNodes int

	Best         *SimulationResult
	Outcome      RunOutcome
	PlanName     string
	MovesCreated int
	MovesFailed  int

	CacheSync  time.Duration
	Simulation time.Duration
	Apply      time.Duration
}

func (s *RunSummary) Log() {
	fields := []zap.Field{
		zap.String("outcome", string(s.Outcome)),
		zap.Bool("dryRun", Config.DryRun),
		zap.Int("workerNodes", s.WorkerNodes),
		zap.Int("pods", s.Pods),
		zap.Int("candidateNodes", s.CandidateNodes),
		zap.Int("candidateSets", s.CandidateSets),
		zap.Int("evictedPods", s.EvictedPods),
		zap.Int("simulations", s.Permutations),
		zap.Int("simulationsFailed", s.SimulationsFailed),
		zap.Int("simulationsWithFreedNodes", s.SimulationsWithFreedNodes),
		zap.Int("threshold", Config.Score.AutoConsolidationThreshold),
		zap.Duration("cacheSyncSeconds", s.CacheSync),
		zap.Duration("simulationSeconds", s.Simulation),
		zap.Duration("applySeconds", s.Apply),
		zap.Duration("totalSeconds", time.Since(s.Start)),
	}
	if s.Best != nil {
		fields = append(fields,
			zap.Int("set", s.Best.SetIndex),
			zap.Int("perm", s.Best.PermIndex),
			zap.String("strategy", permutationStrategyOf(s.Best)),
			zap.Int("freedNodes", s.Best.FreedNodes),
			zap.Int("moves", len(s.Best.Moves)),
			zap.Int("cost", s.Best.Cost),
			zap.Int("score", s.Best.Score),
		)
	}
	if s.Outcome == OutcomeApplied || s.Outcome == OutcomeApplyFailed {
		fields = append(fields,
			zap.String("plan", s.PlanName),
			zap.Int("podMovesCreated", s.MovesCreated),
			zap.Int("podMovesFailed", s.MovesFailed),
		)
	}
	log.Info("Run summary", fields...)
}
