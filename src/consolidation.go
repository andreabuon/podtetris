package main

import (
	"context"
	"fmt"
	"log"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type PodMove struct {
	pod          *apiv1.Pod
	fromNodeName string
	toNodeName   string
	cost         int
}

func (pm PodMove) String() string {
	return fmt.Sprintf("Pod '%s' moved from node '%s' to '%s' with a move cost of %d", pm.pod.Name, pm.fromNodeName, pm.toNodeName, pm.cost)
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

	log.Printf("Consolidation completed. %d pod moves applied", len(appliedMoves))
}

func applyPodMove(ctx context.Context, clientset kubernetes.Interface, podMove PodMove) error {
	original := podMove.pod

	newPod := original.DeepCopy()
	newPod.Name = original.Name + "-podtetris"
	newPod.GenerateName = ""
	newPod.ResourceVersion = ""
	newPod.UID = ""
	newPod.Status = apiv1.PodStatus{}
	newPod.Spec.NodeName = podMove.toNodeName

	log.Printf("Trying to create pod %s...xw on namespace %s", newPod.Name, newPod.Namespace)

	created, err := clientset.CoreV1().Pods(newPod.Namespace).Create(ctx, newPod, metav1.CreateOptions{})
	if err != nil {
		log.Printf("Error during pod creation: %s", err)
		return err
	}

	log.Printf("Created pod '%s' on namespace '%s'", created.Name, newPod.Namespace)

	log.Printf("Deleting original pod %s...", original.Name)
	err = clientset.CoreV1().Pods(original.Namespace).Delete(ctx, original.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("Warning: replacement pod '%s' created, but failed to delete original pod '%s': %v", created.Name, original.Name, err)
		return err
	}
	log.Printf("Original pod '%s' deleted", original.Name)

	return nil
}
