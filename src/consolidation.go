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
	podToMove := podMove.pod

	//scale the deployment replicas up
	//the deployment will create a new pod, the mutating webhook will patch the new pod by applying the previosuly computed spec.NodeName and nodeSelector
	log.Printf("Scaling up pod %s deployment...", podToMove.Name)
	err := scalePodDeployment(ctx, clientset, podToMove, 1)
	if err != nil {
		return fmt.Errorf("error while scaling pod deployment up: %v", err)
	}

	log.Printf("Deleting original pod %s...", podToMove.Name)
	err = clientset.CoreV1().Pods(podToMove.Namespace).Delete(ctx, podToMove.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("Failed to delete original pod '%s': %v", podToMove.Name, err)
		return err
	}
	log.Printf("Original pod '%s' deleted", podToMove.Name)

	//scale deployment replicas down
	log.Printf("Scaling down pod %s deployment...", podToMove.Name)
	err = scalePodDeployment(ctx, clientset, podToMove, -1)
	if err != nil {
		return fmt.Errorf("error while scaling pod deployment down: %v", err)
	}

	return nil
}

func scalePodDeployment(ctx context.Context, clientset kubernetes.Interface, pod *apiv1.Pod, delta int32) error {
	var replicaSetName string
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" && *owner.Controller {
			replicaSetName = owner.Name
			break
		}
	}

	if replicaSetName == "" {
		return fmt.Errorf("pod %s/%s is not owned by a ReplicaSet", pod.Namespace, pod.Name)
	}

	rs, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, replicaSetName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get replica set %s: %w", replicaSetName, err)
	}

	var deploymentName string = ""
	for _, owner := range rs.OwnerReferences {
		if owner.Kind == "Deployment" && *owner.Controller {
			deploymentName = owner.Name
			break
		}
	}

	if deploymentName == "" {
		return fmt.Errorf("replica set %s/%s is not owned by a Deployment", pod.Namespace, pod.Name)
	}

	apps := clientset.AppsV1()
	deploymentInterface := apps.Deployments(pod.Namespace)

	deployment, err := deploymentInterface.Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	scale, err := deploymentInterface.GetScale(ctx, deployment.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	scale.Spec.Replicas += delta

	_, err = deploymentInterface.UpdateScale(ctx, deployment.Name, scale, metav1.UpdateOptions{})

	return nil
}
