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

// CandidatesConfig controls how candidate node sets are chosen.
type CandidatesConfig struct {
	PercentOfClusterNodes int                     `mapstructure:"percentOfClusterNodes"`
	Min                   int                     `mapstructure:"min"`
	Max                   int                     `mapstructure:"max"`
	Mix                   CandidateNodesMixConfig `mapstructure:"mix"`
	SetsToCreate          int                     `mapstructure:"setsToCreate"`
	SelectionMaxRetries   int                     `mapstructure:"selectionMaxRetries"`
}

// PermutationsConfig controls which pod orderings are simulated.
type PermutationsConfig struct {
	EnabledStrategies []string `mapstructure:"enabledStrategies"`
	RandomCount       int      `mapstructure:"randomCount"`
}

type AppConfig struct {
	PodtetrisNamespace              string             `mapstructure:"podtetrisNamespace"`
	Candidates                      CandidatesConfig   `mapstructure:"candidates"`
	EmptyNodesScoreWeight           int                `mapstructure:"emptyNodesScoreWeight"`
	CostScoreWeight                 int                `mapstructure:"costScoreWeight"`
	AutoConsolidationScoreThreshold int                `mapstructure:"autoConsolidationScoreThreshold"`
	Permutations                    PermutationsConfig `mapstructure:"permutations"`
	Parallelism                     int                `mapstructure:"parallelism"`
	DryRun                          bool               `mapstructure:"dryRun"`
	LogLevel                        string             `mapstructure:"logLevel"`
}

func setDefaultConfigValues() {
	viper.SetDefault("podtetrisNamespace", "podtetris")
	viper.SetDefault("candidates.percentOfClusterNodes", 30)
	viper.SetDefault("candidates.min", 3)
	viper.SetDefault("candidates.max", 20)
	viper.SetDefault("candidates.mix.byCPU", 40)
	viper.SetDefault("candidates.mix.byMemory", 40)
	viper.SetDefault("candidates.mix.random", 20)
	viper.SetDefault("candidates.setsToCreate", 3)
	viper.SetDefault("emptyNodesScoreWeight", 400)
	viper.SetDefault("costScoreWeight", 1)
	viper.SetDefault("autoConsolidationScoreThreshold", 0)
	viper.SetDefault("candidates.selectionMaxRetries", 15)
	viper.SetDefault("permutations.enabledStrategies", []string{"cpu_desc", "memory_desc", "random"})
	viper.SetDefault("permutations.randomCount", 3)
	viper.SetDefault("parallelism", 8)
	viper.SetDefault("dryRun", false)
	viper.SetDefault("logLevel", "info")
}

func validateConfig(cfg *AppConfig) error {
	candidates := cfg.Candidates
	if candidates.PercentOfClusterNodes < 1 || candidates.PercentOfClusterNodes > 100 {
		return fmt.Errorf("candidates.percentOfClusterNodes must be in [1, 100], got %d", candidates.PercentOfClusterNodes)
	}
	if candidates.Min < 1 {
		return fmt.Errorf("candidates.min must be >= 1, got %d", candidates.Min)
	}
	if candidates.Max < candidates.Min {
		return fmt.Errorf("candidates.max (%d) must be >= candidates.min (%d)", candidates.Max, candidates.Min)
	}
	mix := candidates.Mix
	if mix.ByCPU < 0 || mix.ByMemory < 0 || mix.Random < 0 {
		return fmt.Errorf("candidates.mix percentages must be >= 0, got %+v", mix)
	}
	if sum := mix.ByCPU + mix.ByMemory + mix.Random; sum != 100 {
		return fmt.Errorf("candidates.mix percentages must sum to 100, got %d", sum)
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
