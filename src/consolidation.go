package main

import (
	"context"
	"fmt"
	"log"

	apiv1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	pod := podMove.pod

	ownerRef := getControllerReference(pod)
	if ownerRef == nil {
		return fmt.Errorf("No controller found for pod %s", pod.Name)
	}

	if ownerRef.Kind == "StatefulSet" {
		log.Printf("Pod %s managed by a StatefulSet. Applying Eviction...", pod.Name)
		return moveStatefulSetPod(ctx, clientset, pod)
	}

	if ownerRef.Kind == "ReplicaSet" {
		rs, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, ownerRef.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get replica set %s: %w", ownerRef.Name, err)
		}

		rsOwnerRef := getControllerReferenceFromRefs(rs.OwnerReferences)
		if rsOwnerRef != nil && rsOwnerRef.Kind != "Deployment" {
			return fmt.Errorf("pod %s/%s owned by ReplicaSet %s, which is owned by unsupported controller kind %s", pod.Namespace, pod.Name, rs.Name, rsOwnerRef.Kind)
		}

		if rsOwnerRef != nil && rsOwnerRef.Kind == "Deployment" {
			log.Printf("Pod %s managed by a Deployment (via ReplicaSet %s). Applying scale strategy...", pod.Name, rs.Name)
			return moveDeploymentPod(ctx, clientset, pod)
		}

		log.Printf("Pod %s managed by a bare ReplicaSet %s. Applying scale strategy...", pod.Name, rs.Name)
		return moveReplicaSetPod(ctx, clientset, pod)
	}

	return fmt.Errorf("Controller not supported: %s", ownerRef.Kind)
}

func moveStatefulSetPod(ctx context.Context, clientset kubernetes.Interface, pod *apiv1.Pod) error {
	log.Printf("Evicting StatefulSet pod %s...", pod.Name)

	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}

	err := clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
	if err != nil {
		if apierrors.IsTooManyRequests(err) {
			return fmt.Errorf("eviction of pod %s/%s blocked by PodDisruptionBudget: %w", pod.Namespace, pod.Name, err)
		}
		return fmt.Errorf("failed to evict pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}

	log.Printf("Pod '%s' evicted", pod.Name)
	return nil
}

func moveReplicaSetPod(ctx context.Context, clientset kubernetes.Interface, pod *apiv1.Pod) error {
	//scale the replica set replicas up
	//the replica set will create a new pod, the mutating webhook will patch the new pod by applying the previosuly computed spec.NodeName and nodeSelector
	log.Printf("Scaling up pod %s replica set...", pod.Name)
	err := scaleReplicaSet(ctx, clientset, pod, 1)
	if err != nil {
		return fmt.Errorf("error while scaling replica set up: %v", err)
	}

	log.Printf("Deleting original pod %s...", pod.Name)
	err = clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	//TODO replace this with eviction ?
	if err != nil {
		log.Printf("Failed to delete original pod '%s': %v", pod.Name, err)
		return err
	}
	log.Printf("Original pod '%s' deleted", pod.Name)

	//scale replica set replicas down
	log.Printf("Scaling down pod %s replica set...", pod.Name)
	err = scaleReplicaSet(ctx, clientset, pod, -1)
	if err != nil {
		return fmt.Errorf("error while scaling replica set down: %v", err)
	}

	return nil
}

func scaleReplicaSet(ctx context.Context, clientset kubernetes.Interface, pod *apiv1.Pod, delta int32) error {
	ownerRef := getControllerReference(pod)
	if ownerRef == nil {
		return fmt.Errorf("pod %s/%s has no controller owner reference", pod.Namespace, pod.Name)
	}
	if ownerRef.Kind != "ReplicaSet" {
		return fmt.Errorf("pod %s/%s is not owned by a ReplicaSet (owner kind: %s)", pod.Namespace, pod.Name, ownerRef.Kind)
	}

	rsInterface := clientset.AppsV1().ReplicaSets(pod.Namespace)

	rs, err := rsInterface.Get(ctx, ownerRef.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get replica set %s: %w", ownerRef.Name, err)
	}

	scale, err := rsInterface.GetScale(ctx, rs.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale for replica set %s: %w", rs.Name, err)
	}

	scale.Spec.Replicas += delta

	if _, err := rsInterface.UpdateScale(ctx, rs.Name, scale, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update scale for replica set %s: %w", rs.Name, err)
	}

	return nil
}

func moveDeploymentPod(ctx context.Context, clientset kubernetes.Interface, pod *apiv1.Pod) error {
	//scale the deployment replicas up
	//the deployment will create a new pod, the mutating webhook will patch the new pod by applying the previosuly computed spec.NodeName and nodeSelector
	log.Printf("Scaling up pod %s deployment...", pod.Name)
	err := scalePodDeployment(ctx, clientset, pod, 1)
	if err != nil {
		return fmt.Errorf("error while scaling pod deployment up: %v", err)
	}

	log.Printf("Deleting original pod %s...", pod.Name)
	err = clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	//TODO replace this with eviction ?
	if err != nil {
		log.Printf("Failed to delete original pod '%s': %v", pod.Name, err)
		return err
	}
	log.Printf("Original pod '%s' deleted", pod.Name)

	//scale deployment replicas down
	log.Printf("Scaling down pod %s deployment...", pod.Name)
	err = scalePodDeployment(ctx, clientset, pod, -1)
	if err != nil {
		return fmt.Errorf("error while scaling pod deployment down: %v", err)
	}

	return nil
}

func scalePodDeployment(ctx context.Context, clientset kubernetes.Interface, pod *apiv1.Pod, delta int32) error {
	ownerRef := getControllerReference(pod)
	if ownerRef == nil {
		return fmt.Errorf("pod %s/%s has no controller owner reference", pod.Namespace, pod.Name)
	}
	if ownerRef.Kind != "ReplicaSet" {
		return fmt.Errorf("pod %s/%s is not owned by a ReplicaSet (owner kind: %s)", pod.Namespace, pod.Name, ownerRef.Kind)
	}

	rs, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, ownerRef.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get replica set %s: %w", ownerRef.Name, err)
	}

	rsOwnerRef := getControllerReferenceFromRefs(rs.OwnerReferences)
	if rsOwnerRef == nil || rsOwnerRef.Kind != "Deployment" {
		return fmt.Errorf("replica set %s/%s is not owned by a Deployment", pod.Namespace, rs.Name)
	}
	deploymentName := rsOwnerRef.Name

	deploymentInterface := clientset.AppsV1().Deployments(pod.Namespace)

	deployment, err := deploymentInterface.Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment %s: %w", deploymentName, err)
	}

	scale, err := deploymentInterface.GetScale(ctx, deployment.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale for deployment %s: %w", deployment.Name, err)
	}

	scale.Spec.Replicas += delta

	if _, err := deploymentInterface.UpdateScale(ctx, deployment.Name, scale, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update scale for deployment %s: %w", deployment.Name, err)
	}

	return nil
}
