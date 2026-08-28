package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"time"

	podtetrisv1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/predicate"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	kubeframework "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodevolumelimits"
	fwkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var Config AppConfig

// control-plane nodes should never be consolidation candidates.
const nonControlPlaneLabelSelector = "!node-role.kubernetes.io/control-plane"

func main() {
	ctx := context.Background()

	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/podtetris/")
	viper.AddConfigPath(".")
	setDefaultConfigValues()
	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("fatal error config file: %w", err))
	}

	if err := viper.Unmarshal(&Config); err != nil {
		panic(fmt.Errorf("fatal error unmarshalling config: %w", err))
	}

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		// fallback for local dev runs
		kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")
		clusterConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			log.Fatalf("Error loading local cluster config: %v", err)
		}
	}
	clientset, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	// initialize and start informers
	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		30*time.Second,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = nonControlPlaneLabelSelector
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

	// retrieve nodes to build the cluster snapshot
	workerNodeSelector, err := labels.Parse(nonControlPlaneLabelSelector)
	if err != nil {
		log.Fatalf("Error parsing the worker node label selector: %v", err)
	}
	nodes, err := nodeInformer.Lister().List(workerNodeSelector)
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

	initialEmptyNodes := countEmptyNodes(candidateNodes)
	initialPodAllocations := createPodAllocationsMap(candidateNodes)

	evictedPods := virtuallyEvictPods(snapshot, candidateNodes)
	permutations := generatePermutations(evictedPods, Config.EnabledPermutationStrategies)

	initialState := &Baseline{
		CandidateNodes: candidateNodes,
		Allocations:    initialPodAllocations,
		EmptyNodeCount: initialEmptyNodes,
	}

	schedulingSimulator := &SchedulingSimulator{
		framework: realFramework,
		snapshot:  snapshot,
		baseline:  initialState,
	}

	var schedulingResults []*SimulationResult
	for permutationIndex, permutation := range permutations {
		log.Printf("Simulating permutation #%d", permutationIndex)
		podPermutation := &PodOrdering{
			Index: permutationIndex,
			Pods:  permutation,
		}
		schedulingResult, err := schedulingSimulator.Run(ctx, podPermutation)
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

	if bestPermutationResult.Score > Config.AutoConsolidationScoreThreshold {
		log.Printf("Score threshold reached, auto applying consolidation strategy")
		scheme := runtime.NewScheme()
		if err := podtetrisv1.AddToScheme(scheme); err != nil {
			log.Fatalf("Error registering the podtetris scheme: %v", err)
		}
		crdClient, err := client.New(clusterConfig, client.Options{Scheme: scheme})
		if err != nil {
			log.Fatalf("Error creating the podtetris client: %v", err)
		}
		applyConsolidationStrategy(ctx, crdClient, bestPermutationResult)
	}
}

func createPodAllocationsMap(candidateNodes []kubeframework.NodeInfo) map[types.NamespacedName]string {
	podAllocations := make(map[types.NamespacedName]string, len(candidateNodes))

	for _, nodeInfo := range candidateNodes {
		pods := nodeInfo.GetPods()
		for _, podInfo := range pods {
			pod := podInfo.GetPod()
			podName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
			podAllocations[podName] = nodeInfo.Node().Name
		}
	}
	return podAllocations
}
