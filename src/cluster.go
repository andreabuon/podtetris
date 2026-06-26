package main

import (
	"context"
	"log"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

const controlPlaneLabelSelector = "!node-role.kubernetes.io/control-plane"

func initInformerFactory(ctx context.Context, clientset *kubernetes.Clientset) informers.SharedInformerFactory {
	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		0,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = controlPlaneLabelSelector
		}),
	)
	informerFactory.Start(ctx.Done())

	for _, synced := range informerFactory.WaitForCacheSync(ctx.Done()) {
		if !synced {
			log.Fatalf("Informer caches failed to sync")
		}
	}
	return informerFactory
}

func fetchClusterState(ctx context.Context, clientset *kubernetes.Clientset) ([]*apiv1.Node, []*apiv1.Pod) {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: controlPlaneLabelSelector,
	})
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
