package main

import (
	"log"

	"github.com/spf13/viper"
	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
)

const (
	SCHEDULER_CONFIG_PATH = "/etc/podtetris/podtetris-scheduler-config.yaml"
)

type CandidateNodesNumbersConfig struct {
	Random   int `mapstructure:"random"`
	ByCPU    int `mapstructure:"byCPU"`
	ByMemory int `mapstructure:"byMemory"`
}

type AppConfig struct {
	PodtetrisNamespace                string                      `mapstructure:"podtetrisNamespace"`
	CandidateNodesNumbers             CandidateNodesNumbersConfig `mapstructure:"candidateNodesNumbers"`
	CandidateNodesSetsToCreate        int                         `mapstructure:"candidateNodesSetsToCreate"`
	EmptyNodesScoreWeight             int                         `mapstructure:"emptyNodesScoreWeight"`
	CostScoreWeight                   int                         `mapstructure:"costScoreWeight"`
	AutoConsolidationScoreThreshold   int                         `mapstructure:"autoConsolidationScoreThreshold"`
	CandidateNodesSelectionMaxRetries int                         `mapstructure:"candidateNodesSelectionMaxRetries"`
	EnabledPermutationStrategies      []string                    `mapstructure:"enabledPermutationStrategies"`
	RandomPermutationCount            int                         `mapstructure:"randomPermutationCount"`
	Parallelism                       int                         `mapstructure:"parallelism"`
	DryRun                            bool                        `mapstructure:"dryRun"`
}

func setDefaultConfigValues() {
	viper.SetDefault("podtetrisNamespace", "podtetris")
	viper.SetDefault("candidateNodesNumbers.random", 3)
	viper.SetDefault("candidateNodesNumbers.byCPU", 2)
	viper.SetDefault("candidateNodesNumbers.byMemory", 2)
	viper.SetDefault("candidateNodesSetsToCreate", 3)
	viper.SetDefault("emptyNodesScoreWeight", 400)
	viper.SetDefault("costScoreWeight", 1)
	viper.SetDefault("autoConsolidationScoreThreshold", 0)
	viper.SetDefault("candidateNodesSelectionMaxRetries", 15)
	viper.SetDefault("enabledPermutationStrategies", []string{"cpu_desc", "memory_desc", "random"})
	viper.SetDefault("randomPermutationCount", 1)
	viper.SetDefault("parallelism", 8)
	viper.SetDefault("dryRun", false)
}

func loadSchedulerConfig() *scheduler_config.KubeSchedulerConfiguration {
	schedulerConfig, err := cascheduler.ConfigFromPath(SCHEDULER_CONFIG_PATH)
	if err != nil {
		log.Fatalf("Error loading scheduler config: %v", err)
	}
	return schedulerConfig
}
