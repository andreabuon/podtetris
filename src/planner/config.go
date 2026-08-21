package main

import (
	"log"
	"os"

	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"sigs.k8s.io/yaml"
)

const (
	SCHEDULER_CONFIG_PATH = "./podtetris-scheduler-config.yaml"
	// DefaultConfigPath is where the in-cluster ConfigMap is mounted.
	DefaultConfigPath = "/etc/podtetris/config.yaml"
)

var ENABLED_PERMUTATION_GENERATION_STRATEGIES = []string{
	"cpu_desc",
	"memory_desc",
	"random",
}

type AppConfig struct {
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

// loadAppConfig reads planner settings from a YAML file (ConfigMap-mounted in-cluster).
func loadAppConfig(path string) AppConfig {
	cfg := DefaultAppConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("Config file %q not found; using default app configuration", path)
			return cfg
		}
		log.Fatalf("Error reading config file %q: %v", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Error parsing config file %q: %v", path, err)
	}
	return cfg
}

func loadSchedulerConfig() *scheduler_config.KubeSchedulerConfiguration {
	schedulerConfig, err := cascheduler.ConfigFromPath(SCHEDULER_CONFIG_PATH)
	if err != nil {
		log.Fatalf("Error loading scheduler config: %v", err)
	}
	return schedulerConfig
}
