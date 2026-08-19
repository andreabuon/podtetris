package main

import (
	"context"
	"log"
	"sort"
	"time"

	podtetrisv1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/predicate"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	kubeframework "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodevolumelimits"
	fwkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var Config AppConfig

func main() {
	ctx := context.Background()

	kubeconfig := loadKubeConfig()
	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	Config = loadAppConfig(ctx, clientset)

	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		30*time.Second,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = "!node-role.kubernetes.io/control-plane"
		}),
	)
	nodeInformer := informerFactory.Core().V1().Nodes()
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims()
	pvInformer := informerFactory.Core().V1().PersistentVolumes()
	podInformer := informerFactory.Core().V1().Pods()
	csiNodeInformer := informerFactory.Storage().V1().CSINodes()
	sharedCSIManager := nodevolumelimits.NewCSIManager(csiNodeInformer.Lister())
	_ = nodeInformer.Informer()
	_ = pvcInformer.Informer()
	_ = pvInformer.Informer()
	_ = podInformer.Informer()
	_ = csiNodeInformer.Informer()

	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)
	informerFactory.WaitForCacheSync(stopCh)

	schedulerConfig := loadSchedulerConfig()
	fwHandle, err := framework.NewHandle(informerFactory, schedulerConfig, false, true)
	if err != nil {
		log.Fatalf("Error creating framework handle: %v", err)
	}

	const nonControlPlaneLabelSelector = "!node-role.kubernetes.io/control-plane"
	nodes, err := nodeInformer.Lister().List(labels.Everything())
	if err != nil {
		log.Fatalf("Error retrieving nodes from the informer: %v", err)
	}
	pods, err := podInformer.Lister().List(labels.Everything())
	if err != nil {
		log.Fatalf("Error retrieving pods from the informer: %v", err)
	}

	snapshotStore := store.NewDeltaSnapshotStore(Config.Parallelism)
	snapshot := predicate.NewPredicateSnapshot(snapshotStore, fwHandle, false, Config.Parallelism, true)
	err = snapshot.SetClusterState(nodes, pods, nil, nil)
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
		fwkruntime.WithSharedCSIManager(sharedCSIManager),
	)
	if err != nil {
		log.Fatalf("Error creating the framework: %v", err)
	}

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatalf("Error listing node infos: %v", err)
	}

	candidateNodes, err := selectCandidateNodes(nodeInfos, Config.RandomCandidateNodesNumber, Config.ByCPUCandidateNodesNumber)
	if err != nil {
		log.Fatalf("Error during the candidate nodes selection: %v", err)
	}

	log.Printf("Selected %d nodes for pods consolidation:", Config.RandomCandidateNodesNumber+Config.ByCPUCandidateNodesNumber)
	for _, ni := range candidateNodes {
		log.Printf(" -> Node: %s (%d pods)", ni.Node().Name, len(ni.GetPods()))
	}

	prevEmptyNodesNum := countEmptyNodes(candidateNodes)
	log.Printf("Before the rescheduling simulation there are %d empty candidate nodes", prevEmptyNodesNum)

	previousPodAllocations := createPodAllocationsMap(candidateNodes)

	evictedPods := virtuallyEvictPods(snapshot, candidateNodes)

	permutations := generatePermutations(evictedPods, ENABLED_PERMUTATION_GENERATION_STRATEGIES)

	initialState := &Baseline{
		CandidateNodes: candidateNodes,
		Allocations:    previousPodAllocations,
	}

	var schedulingResults []*SimulationResult
	for permutationIndex, permutation := range permutations {
		log.Printf("Simulating permutation #%d", permutationIndex)
		strategy := &PodOrdering{
			Index: permutationIndex,
			Pods:  permutation,
		}
		schedulingResult, err := runSchedulingSimulation(ctx, realFramework, snapshot, strategy, *initialState)
		if err != nil {
			log.Printf("Error during scheduling simulation #%d: %v", permutationIndex, err)
			continue
		}

		if schedulingResult.FreedNodes > 0 {
			schedulingResults = append(schedulingResults, schedulingResult)
		}
	}

	log.Println("Simulations results:")
	for _, result := range schedulingResults {
		log.Printf("Strategy #%d freed %d nodes with %d moves, total cost of %d, permutation score is %d", result.Permutation.Index, result.FreedNodes, len(result.Moves), result.Cost, result.Score)
	}

	if len(schedulingResults) < 1 {
		log.Println("No viable consolidation plans found")
		return
	}

	sort.Slice(schedulingResults, func(i, j int) bool {
		return schedulingResults[i].Score > schedulingResults[j].Score
	})
	bestPermutationResult := schedulingResults[0]
	log.Printf("The best consolidation plan is #%d", bestPermutationResult.Permutation.Index)

	/* //TODO Restore the score check
	if bestPermutationResult.score > Config.AutoConsolidationScoreThreshold {
		log.Printf("Score threshold reached, auto applying consolidation strategy")
		scheme := runtime.NewScheme()
		if err := podtetrisv1.AddToScheme(scheme); err != nil {
			log.Fatalf("Error registering the PodMove scheme: %v", err)
		}
		crdClient, err := client.New(kubeconfig, client.Options{Scheme: scheme})
		if err != nil {
			log.Fatalf("Error creating the PodMove client: %v", err)
		}
		applyConsolidationStrategy(ctx, crdClient, bestPermutationResult.moves)
	}
	*/
	//TODO After restoring the score check, delete the following lines
	scheme := runtime.NewScheme()
	if err := podtetrisv1.AddToScheme(scheme); err != nil {
		log.Fatalf("Error registering the PodMove scheme: %v", err)
	}
	crdClient, err := client.New(kubeconfig, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("Error creating the PodMove client: %v", err)
	}
	applyConsolidationStrategy(ctx, crdClient, bestPermutationResult.Moves)
	// delete until here
}

func createPodAllocationsMap(candidateNodes []kubeframework.NodeInfo) map[types.NamespacedName]string {
	previousPodAllocations := make(map[types.NamespacedName]string, len(candidateNodes))

	for _, nodeInfo := range candidateNodes {
		pods := nodeInfo.GetPods()
		for _, podInfo := range pods {
			pod := podInfo.GetPod()
			podName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
			previousPodAllocations[podName] = nodeInfo.Node().Name
		}
	}
	return previousPodAllocations
}
