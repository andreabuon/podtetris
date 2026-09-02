package main

import (
	"log"

	"github.com/spf13/viper"
	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
)

const (
	SCHEDULER_CONFIG_PATH = "./podtetris-scheduler-config.yaml"
)

type AppConfig struct {
	PodtetrisNamespace                string   `mapstructure:"podtetrisNamespace"`
	RandomCandidateNodesNumber        int      `mapstructure:"randomCandidateNodesNumber"`
	ByCPUCandidateNodesNumber         int      `mapstructure:"byCPUCandidateNodesNumber"`
	EmptyNodesScoreWeight             int      `mapstructure:"emptyNodesScoreWeight"`
	CostScoreWeight                   int      `mapstructure:"costScoreWeight"`
	AutoConsolidationScoreThreshold   int      `mapstructure:"autoConsolidationScoreThreshold"`
	CandidateNodesSelectionMaxRetries int      `mapstructure:"candidateNodesSelectionMaxRetries"`
	FixedPodAnnotation                string   `mapstructure:"fixedPodAnnotation"`
	EnabledPermutationStrategies      []string `mapstructure:"enabledPermutationStrategies"`
	Parallelism                       int      `mapstructure:"parallelism"`
}

func setDefaultConfigValues() {
	viper.SetDefault("podtetrisNamespace", "podtetris")
	viper.SetDefault("randomCandidateNodesNumber", 3)
	viper.SetDefault("byCPUCandidateNodesNumber", 2)
	viper.SetDefault("emptyNodesScoreWeight", 400)
	viper.SetDefault("costScoreWeight", 1)
	viper.SetDefault("autoConsolidationScoreThreshold", 0)
	viper.SetDefault("candidateNodesSelectionMaxRetries", 15)
	viper.SetDefault("fixedPodAnnotation", "podtetris/fixed")
	viper.SetDefault("enabledPermutationStrategies", []string{"cpu_desc", "memory_desc", "random"})
	viper.SetDefault("parallelism", 8)
}

func loadSchedulerConfig() *scheduler_config.KubeSchedulerConfiguration {
	schedulerConfig, err := cascheduler.ConfigFromPath(SCHEDULER_CONFIG_PATH)
	if err != nil {
		log.Fatalf("Error loading scheduler config: %v", err)
	}
	return schedulerConfig
}
