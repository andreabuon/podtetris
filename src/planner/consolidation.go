package main

import (
	"context"
	"fmt"

	podtetrisv1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	"go.uber.org/zap"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type PodMove struct {
	pod          *apiv1.Pod
	fromNodeName string
	toNodeName   string
	cost         int
}

func (pm PodMove) String() string {
	return fmt.Sprintf("Pod '%s' moved from '%s' to '%s' (cost = %d)", pm.pod.Name, pm.fromNodeName, pm.toNodeName, pm.cost)
}

func applyConsolidationStrategy(ctx context.Context, c client.Client, result *SimulationResult) {
	plan, err := createConsolidationPlan(ctx, c, result)
	if err != nil {
		log.Error("Failed to create ConsolidationPlan", zap.Error(err))
		return
	}
	log.Info("Created ConsolidationPlan", zap.String("namespace", plan.Namespace), zap.String("name", plan.Name))

	createdCount := 0
	errorsCount := 0

	for _, pm := range result.Moves {
		err := createPodMoveCRD(ctx, c, plan, pm)
		if err != nil {
			log.Error("Failed to create PodMove",
				zap.Error(err),
				zap.String("pod", pm.pod.Name),
				zap.String("namespace", pm.pod.Namespace),
				zap.String("from", pm.fromNodeName),
				zap.String("to", pm.toNodeName),
			)
			errorsCount++
			continue
		}
		createdCount++
	}

	log.Info("Finished creating PodMoves for ConsolidationPlan",
		zap.String("plan", plan.Name),
		zap.Int("created", createdCount),
		zap.Int("errors", errorsCount),
	)
}

func createConsolidationPlan(ctx context.Context, c client.Client, result *SimulationResult) (*podtetrisv1.ConsolidationPlan, error) {
	plan := &podtetrisv1.ConsolidationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    Config.PodtetrisNamespace,
			GenerateName: "consolidationplan-",
		},
		Spec: podtetrisv1.ConsolidationPlanSpec{
			FreedNodes:  result.FreedNodes,
			NodesToFree: result.NodesToFree,
			Cost:        result.Cost,
			Score:       result.Score,
			MoveCount:   len(result.Moves),
		},
	}
	if err := c.Create(ctx, plan); err != nil {
		return nil, fmt.Errorf("creating ConsolidationPlan: %w", err)
	}
	return plan, nil
}

func createPodMoveCRD(ctx context.Context, c client.Client, plan *podtetrisv1.ConsolidationPlan, podMove PodMove) error {
	if podMove.pod == nil {
		return fmt.Errorf("cannot create PodMove for a nil pod")
	}
	pod := podMove.pod

	controllerRef := metav1.GetControllerOf(pod)
	if controllerRef == nil {
		return fmt.Errorf("pod %s/%s has no controller owner", pod.Namespace, pod.Name)
	}
	if controllerRef.UID == "" {
		return fmt.Errorf("pod %s/%s controller owner %s/%s has empty UID", pod.Namespace, pod.Name, controllerRef.Kind, controllerRef.Name)
	}
	pm := &podtetrisv1.PodMove{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: Config.PodtetrisNamespace,
			Name:      fmt.Sprintf("%s-%s", pod.Name, string(pod.UID)[:8]),
			Labels: map[string]string{
				podtetrisv1.ConsolidationPlanLabelKey: plan.Name,
				podtetrisv1.OwnerUIDLabelKey:          string(controllerRef.UID),
			},
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
	if err := controllerutil.SetControllerReference(plan, pm, c.Scheme()); err != nil {
		return fmt.Errorf("setting ConsolidationPlan owner on PodMove %s/%s: %w", pm.Namespace, pm.Name, err)
	}
	if err := c.Create(ctx, pm); err != nil {
		if apierrors.IsAlreadyExists(err) {
			log.Info("PodMove already exists, skipping", zap.String("namespace", pm.Namespace), zap.String("name", pm.Name))
			return nil
		}
		return fmt.Errorf("creating PodMove %s/%s: %w", pm.Namespace, pm.Name, err)
	}
	return nil
}
