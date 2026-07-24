package main

import (
	"context"
	"log"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const controlPlaneLabelSelector = "!node-role.kubernetes.io/control-plane"

func fetchClusterState(ctx context.Context, clientset *kubernetes.Clientset) ([]*apiv1.Node, []*apiv1.Pod) {
	if clientset == nil {
		log.Fatal("No clientset provided to fetchClusterState")
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster nodes: %v", err)
	}

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster pods: %v", err)
	}

	nodePointers := make([]*apiv1.Node, len(nodes.Items))
	for i := range nodes.Items {
		nodePointers[i] = &nodes.Items[i]
	}

	podPointers := make([]*apiv1.Pod, len(pods.Items))
	for i := range pods.Items {
		podPointers[i] = &pods.Items[i]
	}

	return nodePointers, podPointers
}
