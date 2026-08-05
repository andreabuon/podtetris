package main

import (
	"context"
	"fmt"
	"log"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

type PodMove struct {
	pod          *apiv1.Pod
	fromNodeName string
	toNodeName   string
	cost         int
}

func (pm PodMove) String() string {
	return fmt.Sprintf("Pod '%s' moved from '%s' to '%s' (move cost = %d)", pm.pod.Name, pm.fromNodeName, pm.toNodeName, pm.cost)
}

func applyConsolidationStrategy(ctx context.Context, clientset kubernetes.Interface, podMoves []PodMove) {
	log.Printf("Applying consolidation strategy...")

	createdCount := 0
	errorsCount := 0

	for _, pm := range podMoves {
		err := createPodMoveCRD(ctx, clientset, pm)
		if err != nil {
			log.Printf("error while creating PodMove CRD %s: %v", pm, err)
			errorsCount++
			//TODO undo(appliedMoves) ??
			continue
		}
		createdCount++
	}

	log.Printf("Consolidation completed. %d pod moves created, %d errors.", createdCount, errorsCount)
}

func createPodMoveCRD(ctx context.Context, clientset kubernetes.Interface, podMove PodMove) error {
	return nil
}
