package main

import (
	"context"
	"fmt"
	"log"

	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/predicate"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

func main() {
	klog.InitFlags(nil)
	ctx := context.Background()

	kubeconfig := loadKubeConfig()
	schedulerConfig := loadSchedulerConfig()

	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	informerFactory := initInformerFactory(ctx, clientset)

	fwHandle, err := framework.NewHandle(informerFactory, schedulerConfig, false, false)
	if err != nil {
		log.Fatalf("Error creating framework handle: %v", err)
	}

	// CREATE THE CLUSTER STATE SNAPSHOT

	fmt.Println("Reading live state from active Kubernetes cluster...")
	nodesPointers, podsPointers := fetchClusterState(ctx, clientset)
	fmt.Printf("Cluster discovery completed. Found %d Nodes and %d Pods.\n", len(nodesPointers), len(podsPointers))

	snapshotStore := store.NewDeltaSnapshotStore(PARALLELISM)
	snapshot := predicate.NewPredicateSnapshot(snapshotStore, fwHandle, false, PARALLELISM, false)
	err = snapshot.SetClusterState(nodesPointers, podsPointers, nil, nil)
	if err != nil {
		log.Fatalf("Critical sandbox simulation failure during instantiation: %v", err)
	}

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatalf("Error listing node infos: %v", err)
	}
	fmt.Printf("\nThe cluster contains %d nodes.\n", len(nodeInfos))

	candidateNodes := selectCandidateNodes(nodeInfos)

	// Evict pods from candidate nodes
	fmt.Printf("\nSelected %d Least-Allocated Nodes for pods consolidation:\n", CANDIDATE_NODES_NUMBER)
	for _, ni := range candidateNodes {
		fmt.Printf(" -> Node: %s (Current Pods: %d)\n", ni.Node().Name, len(ni.GetPods()))
	}

	var evictedPods = virtuallyEvictPods(snapshot, candidateNodes)

	permutations := generatePermutations(evictedPods)

	runSchedulingSimulation(snapshot, permutations)

}
