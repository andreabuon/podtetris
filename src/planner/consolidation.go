package main

import (
	"context"
	"fmt"
	"log"

	podtetrisv1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func applyConsolidationStrategy(ctx context.Context, client client.Client, podMoves []PodMove) {
	createdCount := 0
	errorsCount := 0

	for _, pm := range podMoves {
		err := createPodMoveCRD(ctx, client, pm)
		if err != nil {
			log.Printf("Error while creating PodMove CRD %s: %v", pm, err)
			errorsCount++
			continue
		}
		createdCount++
	}

	log.Printf("Consolidation completed, %d PodMoves created, %d errors", createdCount, errorsCount)
}

func createPodMoveCRD(ctx context.Context, c client.Client, podMove PodMove) error {
	if podMove.pod == nil {
		return fmt.Errorf("cannot create PodMove for a nil pod")
	}
	pod := podMove.pod

	controllerRef := metav1.GetControllerOf(pod)
	if controllerRef == nil {
		return fmt.Errorf("pod %s/%s has no controller owner", pod.Namespace, pod.Name)
	}
	pm := &podtetrisv1.PodMove{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: Config.PodtetrisNamespace,
			Name:      fmt.Sprintf("%s-%s", pod.Name, string(pod.UID)[:8]),
		},
		Spec: podtetrisv1.PodMoveSpec{
			Owner: *controllerRef,
			Pod: apiv1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Pod",
				Namespace:  pod.Namespace,
				Name:       pod.Name,
				UID:        pod.UID,
			},
			SourceNode: podMove.fromNodeName,
			TargetNode: podMove.toNodeName,
		},
	}
	if err := c.Create(ctx, pm); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Printf("PodMove %s/%s already exists, skipping", pm.Namespace, pm.Name)
			return nil
		}
		return fmt.Errorf("creating PodMove %s/%s: %w", pm.Namespace, pm.Name, err)
	}
	return nil
}
