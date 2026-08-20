package main

import (
	"log"

	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
)

const (
	SCHEDULER_CONFIG_PATH = "./scheduler-config.yaml"
)

var ENABLED_PERMUTATION_GENERATION_STRATEGIES = []string{
	"cpu_desc",
	"memory_desc",
	"random",
}

type AppConfig struct {
	// Namespace where PODTetris components, PodMoves and ConfigMap are expected to be found.
	// Set at runtime (from the pod's service-account namespace), not from the ConfigMap!
	PodtetrisNamespace                string   `yaml:"-"`
	RandomCandidateNodesNumber        int      `yaml:"randomCandidateNodesNumber"`
	ByCPUCandidateNodesNumber         int      `yaml:"byCPUCandidateNodesNumber"`
	PodMoveDefaultCost                int      `yaml:"podMoveDefaultCost"`
	EmptyNodesScoreWeight             int      `yaml:"emptyNodesScoreWeight"`
	CostScoreWeight                   int      `yaml:"costScoreWeight"`
	AutoConsolidationScoreThreshold   int      `yaml:"autoConsolidationScoreThreshold"`
	CandidateNodesSelectionMaxRetries int      `yaml:"candidateNodesSelectionMaxRetries"`
	FixedPodAnnotation                string   `yaml:"fixedPodAnnotation"`
	PodMoveCostAnnotation             string   `yaml:"podMoveCostAnnotation"`
	EnabledPermutationStrategies      []string `yaml:"enabledPermutationStrategies"`
	Parallelism                       int      `yaml:"parallelism"`
}

// DefaultAppConfig returns an AppConfig populated with sane defaults.
func DefaultAppConfig() AppConfig {
	return AppConfig{
		PodtetrisNamespace:                "podtetris",
		RandomCandidateNodesNumber:        3,
		ByCPUCandidateNodesNumber:         2,
		PodMoveDefaultCost:                10,
		EmptyNodesScoreWeight:             400,
		CostScoreWeight:                   1,
		AutoConsolidationScoreThreshold:   0,
		CandidateNodesSelectionMaxRetries: 15,
		FixedPodAnnotation:                "podtetris/fixed",
		PodMoveCostAnnotation:             "podtetris/moveCost",
		EnabledPermutationStrategies:      ENABLED_PERMUTATION_GENERATION_STRATEGIES,
		Parallelism:                       8,
	}
}

func loadSchedulerConfig() *scheduler_config.KubeSchedulerConfiguration {
	schedulerConfig, err := cascheduler.ConfigFromPath(SCHEDULER_CONFIG_PATH)
	if err != nil {
		log.Fatalf("Error loading scheduler config: %v", err)
	}
	return schedulerConfig
}
