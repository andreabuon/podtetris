package main

import (
	"context"
	"log"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"sigs.k8s.io/yaml"
)

const (
	SCHEDULER_CONFIG_PATH = "scheduler-config.yaml"
)

var ENABLED_PERMUTATION_GENERTATION_STRATEGIES = []string{
	"cpu_desc",
	"memory_desc",
	"random",
}

type AppConfig struct {
	CandidateNodesNumber              int      `yaml:"candidateNodesNumber"`
	Parallelism                       int      `yaml:"parallelism"`
	EmptyNodesScoreWeight             int      `yaml:"emptyNodesScoreWeight"`
	CostScoreWeight                   int      `yaml:"costScoreWeight"`
	AutoConsolidationScoreThreshold   int      `yaml:"autoConsolidationThreshold"`
	PodMoveCost                       int      `yaml:"podMoveCost"`
	CandidateNodesSelectionMaxRetries int      `yaml:"candidateNodesSelectionMaxRetries"`
	FixedPodAnnotation                string   `yaml:"fixedPodAnnotation"`
	PodMoveCostAnnotation             string   `yaml:"podMoveCostAnnotation"`
	EnabledPermutationStrategies      []string `yaml:"enabledPermutationStrategies"`
}

// DefaultAppConfig returns an AppConfig populated with sane defaults.
func DefaultAppConfig() AppConfig {
	return AppConfig{
		CandidateNodesNumber:              5,
		Parallelism:                       8,
		EmptyNodesScoreWeight:             100,
		CostScoreWeight:                   100,
		AutoConsolidationScoreThreshold:   12345,
		PodMoveCost:                       10,
		CandidateNodesSelectionMaxRetries: 15,
		FixedPodAnnotation:                "podtetris/fixed",
		PodMoveCostAnnotation:             "podtetris/moveCost",
		EnabledPermutationStrategies:      ENABLED_PERMUTATION_GENERTATION_STRATEGIES,
	}
}

// loadAppConfig loads the configuration from a ConfigMap and overrides individual fields.
func loadAppConfig(ctx context.Context, clientset *kubernetes.Clientset) AppConfig {
	cfg := DefaultAppConfig()

	cm, err := clientset.CoreV1().ConfigMaps("podtetris").Get(ctx, "podtetris-config", metav1.GetOptions{})
	if err != nil {
		log.Printf("Error reading ConfigMap: %v", err)
		return cfg
	}

	data, ok := cm.Data["config.yaml"]
	if !ok {
		log.Fatalf("Key 'config.yaml' not found in ConfigMap")
	}

	if err := yaml.Unmarshal([]byte(data), &cfg); err != nil {
		log.Fatalf("Error parsing config from ConfigMap: %v", err)
	}
	return cfg
}

func loadKubeConfig() *rest.Config {
	kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		log.Fatalf("Error loading local kubeconfig: %v", err)
	}
	return config
}

func loadSchedulerConfig() *scheduler_config.KubeSchedulerConfiguration {
	schedulerConfig, err := cascheduler.ConfigFromPath(SCHEDULER_CONFIG_PATH)
	if err != nil {
		log.Fatalf("Error loading scheduler config: %v", err)
	}
	return schedulerConfig
}
