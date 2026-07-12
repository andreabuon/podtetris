package main

import (
	"context"
	"fmt"
	"log"

	"k8s.io/client-go/kubernetes"
)

type PodMove struct {
	podName      string
	fromNodeName string
	toNodeName   string
	cost         int
}

func (pm PodMove) String() string {
	return fmt.Sprintf("Pod '%s' moved from node '%s' to '%s' with a move cost of %d", pm.podName, pm.fromNodeName, pm.toNodeName, pm.cost)
}

func applyConsolidationStrategy(ctx context.Context, clientset kubernetes.Interface, podMoves []PodMove) {
	log.Printf("Applying consolidation strategy...")

	var appliedMoves []PodMove

	for _, pm := range podMoves {
		err := applyPodMove(ctx, clientset, pm)
		if err != nil {
			log.Printf("Error while applying pod move %s", pm)
			//TODO undo(appliedMoves) ??
			continue
		}
		appliedMoves = append(appliedMoves, pm)
	}

	log.Print("Consolidation completed. %d pod moves applied", len(appliedMoves))
}

func applyPodMove(ctx context.Context, clientset kubernetes.Interface, podMove PodMove) error {
	//TODO
}
