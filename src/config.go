package main

import (
	"log"
	"path/filepath"

	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	scheduler_config "k8s.io/kubernetes/pkg/scheduler/apis/config"
)

const (
	SCHEDULER_CONFIG_PATH                 = "scheduler-config.yaml"
	CANDIDATE_NODES_NUMBER                = 5
	PARALLELISM                           = 8
	EMPTY_NODES_SCORE_WEIGHT              = 100
	COST_SCORE_WEIGHT                     = 100
	AUTO_CONSOLIDATION_THRESHOLD          = 12345
	POD_MOVE_COST                         = 10
	CANDIDATE_NODES_SELECTION_MAX_RETRIES = 15
	FIXED_POD_ANNOTATION                  = "reply.com/podtetris/fixed"
)

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
