package main

import (
	"context"
	"log"
	"sort"

	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/predicate"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	kubeframework "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins"
	fwkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
)

var Config AppConfig

func main() {
	klog.InitFlags(nil)
	ctx := context.Background()

	kubeconfig := loadKubeConfig()
	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	Config = loadAppConfig(ctx, clientset)

	informerFactory := initInformerFactory(ctx, clientset)
	schedulerConfig := loadSchedulerConfig()
	fwHandle, err := framework.NewHandle(informerFactory, schedulerConfig, false, false)
	if err != nil {
		log.Fatalf("Error creating framework handle: %v", err)
	}

	log.Print("Reading live state from active Kubernetes cluster... ")
	nodesPointers, podsPointers := fetchClusterState(ctx, clientset)
	log.Printf("Found %d Nodes and %d Pods.", len(nodesPointers), len(podsPointers))

	snapshotStore := store.NewDeltaSnapshotStore(Config.Parallelism)
	snapshot := predicate.NewPredicateSnapshot(snapshotStore, fwHandle, false, Config.Parallelism, false)
	err = snapshot.SetClusterState(nodesPointers, podsPointers, nil, nil)
	if err != nil {
		log.Fatalf("Critical sandbox simulation failure during instantiation: %v", err)
	}

	registry := plugins.NewInTreeRegistry()

	realFramework, err := fwkruntime.NewFramework(
		ctx,
		registry,
		&schedulerConfig.Profiles[0],
		fwkruntime.WithInformerFactory(informerFactory),
		fwkruntime.WithSnapshotSharedLister(snapshotStore),
	)
	if err != nil {
		log.Fatalf("Error creating the Framework: %v", err)
	}

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatalf("Error listing node infos: %v", err)
	}

	candidateNodes, err := selectCandidateNodes(nodeInfos, Config.CandidateNodesNumber)
	if err != nil {
		log.Fatalf("Error during the candidate nodes selection: %v", err)
	}

	log.Printf("Selected %d Least-Allocated Nodes for Pods consolidation:", Config.CandidateNodesNumber)
	for _, ni := range candidateNodes {
		log.Printf(" -> Node: %s (Current Pods: %d)", ni.Node().Name, len(ni.GetPods()))
	}

	prevEmptyNodesNum := 0
	for _, candidate := range candidateNodes {
		if isConsideredEmpty(candidate) {
			prevEmptyNodesNum++
		}
	}
	log.Printf("Before the rescheduling simulation there are %d empty candidate nodes.", prevEmptyNodesNum)

	previousPodAllocations := createPodAllocationsMap(candidateNodes)

	var evictedPods = virtuallyEvictPods(snapshot, candidateNodes)

	permutations := generatePermutations(evictedPods, ENABLED_PERMUTATION_GENERTATION_STRATEGIES)

	log.Println("Simulating scheduling of the pods permutations")
	var schedulingResults []*SchedulingResult
	for permutationIndex, permutation := range permutations {
		log.Printf("Testing permutation #%d...", permutationIndex)
		schedulingResult, err := runSchedulingSimulation(realFramework, snapshot, permutation, candidateNodes, previousPodAllocations, prevEmptyNodesNum, ctx)
		if err != nil {
			log.Printf("Error during scheduling simulation #%d: %v", permutationIndex, err)
			continue
		}

		if schedulingResult.emptyNodesNum > 0 {
			schedulingResults = append(schedulingResults, schedulingResult)
		}
	}

	log.Println("Scheduling results:")
	for index, result := range schedulingResults {
		log.Printf("Permutation #%d freed %d nodes with a total cost of %d", index, result.emptyNodesNum, result.cost)
	}

	if len(schedulingResults) < 1 {
		log.Println("The simulation finished with no viable scheduling results.")
		return
	}

	sort.Slice(schedulingResults, func(i, j int) bool {
		return schedulingResults[i].score > schedulingResults[j].score
	})

	bestPermutationResult := schedulingResults[0]
	log.Printf("The best scheduling simulation has a cost of %d.", bestPermutationResult.cost)

	if bestPermutationResult.score > Config.AutoConsolidationScoreThreshold {
		log.Printf("Auto applying consolidation...")
		//executeConsolidation()
	}
}

func createPodAllocationsMap(candidateNodes []kubeframework.NodeInfo) map[string]string {
	previousPodAllocations := make(map[string]string, len(candidateNodes))

	for _, nodeInfo := range candidateNodes {
		pods := nodeInfo.GetPods()
		for _, podInfo := range pods {
			pod := podInfo.GetPod()
			mapKey := pod.Namespace + "/" + pod.Name
			previousPodAllocations[mapKey] = nodeInfo.Node().Name
		}
	}
	return previousPodAllocations
}
