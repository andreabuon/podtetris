package main

import (
	"context"
	"path/filepath"
	"time"

	podtetrisv1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	"github.com/spf13/viper"
	"go.uber.org/zap"
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

var log *zap.Logger

var Config AppConfig

// control-plane nodes should never be consolidation candidates.
const nonControlPlaneLabelSelector = "!node-role.kubernetes.io/control-plane"

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()
	log = logger.Named("planner")

	ctx := context.Background()

	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/podtetris/")
	viper.AddConfigPath(".")
	setDefaultConfigValues()
	err = viper.ReadInConfig()
	if err != nil {
		log.Fatal("Failed to load config file", zap.Error(err))
	}

	if err := viper.Unmarshal(&Config); err != nil {
		log.Fatal("Failed to unmarshal config", zap.Error(err))
	}

	rules, err := loadRulesConfig()
	if err != nil {
		log.Fatal("Failed to load planner rules config", zap.Error(err))
	}

	if Config.DryRun {
		log.Info("Dry run mode enabled: consolidation plans will be computed but not applied")
	}

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		// fallback for local dev runs
		kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")
		clusterConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			log.Fatal("Failed to load local cluster config", zap.Error(err))
		}
	}
	clientset, err := kubernetes.NewForConfig(clusterConfig)
	if err != nil {
		log.Fatal("Failed to create Kubernetes clientset", zap.Error(err))
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
		log.Fatal("Failed to create framework handle", zap.Error(err))
	}

	// retrieve nodes to build the cluster snapshot
	workerNodeSelector, err := labels.Parse(nonControlPlaneLabelSelector)
	if err != nil {
		log.Fatal("Failed to parse worker node label selector", zap.Error(err))
	}
	nodes, err := nodeInformer.Lister().List(workerNodeSelector)
	if err != nil {
		log.Fatal("Failed to list nodes from informer", zap.Error(err))
	}
	pods, err := podInformer.Lister().List(labels.Everything())
	if err != nil {
		log.Fatal("Failed to list pods from informer", zap.Error(err))
	}

	snapshotStore := store.NewDeltaSnapshotStore(Config.Parallelism)
	snapshot := predicate.NewPredicateSnapshot(snapshotStore, fwHandle, false, Config.Parallelism, true)
	err = snapshot.SetClusterState(nodes, pods, nil, nil)
	if err != nil {
		log.Fatal("Failed to instantiate cluster snapshot", zap.Error(err))
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
		log.Fatal("Failed to create scheduler framework", zap.Error(err))
	}

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatal("Failed to list node infos", zap.Error(err))
	}

	candidateNodesSets, err := createCandidateNodesSets(
		nodeInfos,
		Config.CandidateNodesSetsToCreate,
		Config.CandidateNodesNumbers.Random,
		Config.CandidateNodesNumbers.ByCPU,
		Config.CandidateNodesNumbers.ByMemory,
		rules,
	)
	if err != nil {
		log.Fatal("Failed to select candidate nodes", zap.Error(err))
	}

	log.Info("Generated candidate node sets", zap.Int("count", len(candidateNodesSets)))
	for setIndex, candidateSet := range candidateNodesSets {
		log.Debug("Candidate node set", zap.Int("set", setIndex), zap.Strings("nodes", nodeInfoNames(candidateSet.UnsortedList())))
	}

	var bestSimulationResult *SimulationResult

	for setIndex, candidateSet := range candidateNodesSets {
		candidateNodes := candidateSet.UnsortedList()

		// Each node set must start from a clean baseline; virtuallyEvictPods mutates the snapshot.
		snapshot.Fork()

		initialState := &Baseline{
			CandidateNodes:    candidateNodes,
			Allocations:       createPodAllocationsMap(candidateNodes),
			InitialEmptyNodes: getEmptyNodes(candidateNodes, rules),
		}

		evictedPods := virtuallyEvictPods(snapshot, candidateNodes, rules)
		permutations := generatePermutations(evictedPods, Config.EnabledPermutationStrategies, Config.RandomPermutationCount)

		schedulingSimulator := &SchedulingSimulator{
			framework: realFramework,
			snapshot:  snapshot,
			baseline:  initialState,
			rules:     rules,
		}

		for permutationIndex, permutation := range permutations {
			id := SimulationID{SetIndex: setIndex, PermIndex: permutationIndex}
			log.Debug("Running simulation", zap.Int("set", id.SetIndex), zap.Int("perm", id.PermIndex))
			podPermutation := &PodOrdering{
				Index: permutationIndex,
				Pods:  permutation,
			}
			schedulingResult, err := schedulingSimulator.Run(ctx, podPermutation)
			if err != nil {
				log.Debug("Simulation failed", zap.Int("set", id.SetIndex), zap.Int("perm", id.PermIndex), zap.Error(err))
				continue
			}

			schedulingResult.SimulationID = id
			schedulingResult.CandidateNodes = candidateNodes

			if schedulingResult.FreedNodes < 1 {
				continue
			}

			if bestSimulationResult == nil {
				bestSimulationResult = schedulingResult
				continue
			}

			if schedulingResult.Score > bestSimulationResult.Score {
				bestSimulationResult = schedulingResult
				continue
			}
		}

		snapshot.Revert()
	}

	log.Info("Finished simulations")

	if bestSimulationResult == nil {
		log.Info("No viable consolidation plan found")
		return
	}

	log.Info("Selected best consolidation plan",
		zap.Int("set", bestSimulationResult.SetIndex),
		zap.Int("perm", bestSimulationResult.PermIndex),
		zap.Int("freedNodes", bestSimulationResult.FreedNodes),
		zap.Strings("nodesToFree", bestSimulationResult.NodesToFree),
		zap.Int("moves", len(bestSimulationResult.Moves)),
		zap.Int("cost", bestSimulationResult.Cost),
		zap.Int("score", bestSimulationResult.Score),
	)

	if Config.DryRun {
		log.Info("Skipping apply because dry run is enabled")
		return
	}

	if bestSimulationResult.Score > Config.AutoConsolidationScoreThreshold {
		log.Info("Score threshold reached, applying consolidation plan",
			zap.Int("score", bestSimulationResult.Score),
			zap.Int("threshold", Config.AutoConsolidationScoreThreshold),
		)
		scheme := runtime.NewScheme()
		if err := podtetrisv1.AddToScheme(scheme); err != nil {
			log.Fatal("Failed to register podtetris scheme", zap.Error(err))
		}
		crdClient, err := client.New(clusterConfig, client.Options{Scheme: scheme})
		if err != nil {
			log.Fatal("Failed to create podtetris client", zap.Error(err))
		}
		applyConsolidationStrategy(ctx, crdClient, bestSimulationResult)
	} else {
		log.Info("Best plan score below auto-consolidation threshold, skipping apply",
			zap.Int("score", bestSimulationResult.Score),
			zap.Int("threshold", Config.AutoConsolidationScoreThreshold),
		)
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

func nodeInfoNames(nodes []kubeframework.NodeInfo) []string {
	names := make([]string, len(nodes))
	for i, node := range nodes {
		names[i] = node.Node().Name
	}
	return names
}
