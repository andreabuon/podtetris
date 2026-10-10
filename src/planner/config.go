package main

import (
	"fmt"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
)

const (
	SCHEDULER_CONFIG_PATH = "/etc/podtetris/podtetris-scheduler-config.yaml"
)

// CandidateNodesMixConfig holds the percentage of candidate nodes chosen by each selection strategy.
// The percentages must sum to 100.
type CandidateNodesMixConfig struct {
	ByCPU    int `mapstructure:"byCPU"`
	ByMemory int `mapstructure:"byMemory"`
	Random   int `mapstructure:"random"`
}

type AppConfig struct {
	PodtetrisNamespace                string                  `mapstructure:"podtetrisNamespace"`
	CandidateNodesPercent             int                     `mapstructure:"candidateNodesPercent"`
	CandidateNodesMin                 int                     `mapstructure:"candidateNodesMin"`
	CandidateNodesMax                 int                     `mapstructure:"candidateNodesMax"`
	CandidateNodesMix                 CandidateNodesMixConfig `mapstructure:"candidateNodesMix"`
	CandidateNodesSetsToCreate        int                     `mapstructure:"candidateNodesSetsToCreate"`
	EmptyNodesScoreWeight             int                     `mapstructure:"emptyNodesScoreWeight"`
	CostScoreWeight                   int                     `mapstructure:"costScoreWeight"`
	AutoConsolidationScoreThreshold   int                     `mapstructure:"autoConsolidationScoreThreshold"`
	CandidateNodesSelectionMaxRetries int                     `mapstructure:"candidateNodesSelectionMaxRetries"`
	EnabledPermutationStrategies      []string                `mapstructure:"enabledPermutationStrategies"`
	RandomPermutationCount            int                     `mapstructure:"randomPermutationCount"`
	Parallelism                       int                     `mapstructure:"parallelism"`
	DryRun                            bool                    `mapstructure:"dryRun"`
	LogLevel                          string                  `mapstructure:"logLevel"`
}

func setDefaultConfigValues() {
	viper.SetDefault("podtetrisNamespace", "podtetris")
	viper.SetDefault("candidateNodesPercent", 30)
	viper.SetDefault("candidateNodesMin", 3)
	viper.SetDefault("candidateNodesMax", 20)
	viper.SetDefault("candidateNodesMix.byCPU", 40)
	viper.SetDefault("candidateNodesMix.byMemory", 40)
	viper.SetDefault("candidateNodesMix.random", 20)
	viper.SetDefault("candidateNodesSetsToCreate", 3)
	viper.SetDefault("emptyNodesScoreWeight", 400)
	viper.SetDefault("costScoreWeight", 1)
	viper.SetDefault("autoConsolidationScoreThreshold", 0)
	viper.SetDefault("candidateNodesSelectionMaxRetries", 15)
	viper.SetDefault("enabledPermutationStrategies", []string{"cpu_desc", "memory_desc", "random"})
	viper.SetDefault("randomPermutationCount", 1)
	viper.SetDefault("parallelism", 8)
	viper.SetDefault("dryRun", false)
	viper.SetDefault("logLevel", "info")
}

func validateConfig(cfg *AppConfig) error {
	if cfg.CandidateNodesPercent < 1 || cfg.CandidateNodesPercent > 100 {
		return fmt.Errorf("candidateNodesPercent must be in [1, 100], got %d", cfg.CandidateNodesPercent)
	}
	if cfg.CandidateNodesMin < 1 {
		return fmt.Errorf("candidateNodesMin must be >= 1, got %d", cfg.CandidateNodesMin)
	}
	if cfg.CandidateNodesMax < cfg.CandidateNodesMin {
		return fmt.Errorf("candidateNodesMax (%d) must be >= candidateNodesMin (%d)", cfg.CandidateNodesMax, cfg.CandidateNodesMin)
	}
	mix := cfg.CandidateNodesMix
	if mix.ByCPU < 0 || mix.ByMemory < 0 || mix.Random < 0 {
		return fmt.Errorf("candidateNodesMix percentages must be >= 0, got %+v", mix)
	}
	if sum := mix.ByCPU + mix.ByMemory + mix.Random; sum != 100 {
		return fmt.Errorf("candidateNodesMix percentages must sum to 100, got %d", sum)
	}
	return nil
}

func newLogger(levelName string) (*zap.Logger, error) {
	level, err := zapcore.ParseLevel(levelName)
	if err != nil {
		return nil, err
	}
	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(level)
	return cfg.Build()
}

func loadSchedulerConfig() *scheduler_config.KubeSchedulerConfiguration {
	schedulerConfig, err := cascheduler.ConfigFromPath(SCHEDULER_CONFIG_PATH)
	if err != nil {
		log.Fatal("Failed to load scheduler config", zap.Error(err), zap.String("path", SCHEDULER_CONFIG_PATH))
	}
	return schedulerConfig
}
