package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/predicate"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/framework"
	"k8s.io/autoscaler/cluster-autoscaler/simulator/scheduling"
	cascheduler "k8s.io/autoscaler/cluster-autoscaler/utils/scheduler"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"
)

const SCHEDULER_CONFIG_PATH = "scheduler-config.yaml"
const CANDIDATE_NODES_NUMBER = 5

const PARALLELISM = 8
const YAML_BUFFER_SIZE = 4096

func main() {
	klog.InitFlags(nil)

	kubeconfigPath := filepath.Join(homedir.HomeDir(), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		log.Fatalf("Error loading local kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	schedulerConfig, err := cascheduler.ConfigFromPath(SCHEDULER_CONFIG_PATH)
	if err != nil {
		log.Fatalf("Error loading scheduler config: %v", err)
	}

	ctx := context.Background()
	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	informerFactory.Start(ctx.Done())

	for _, synced := range informerFactory.WaitForCacheSync(ctx.Done()) {
		if !synced {
			log.Fatalf("Informer caches failed to sync")
		}
	}

	fwHandle, err := framework.NewHandle(informerFactory, schedulerConfig, false, false)
	if err != nil {
		log.Fatalf("Error creating framework handle: %v", err)
	}

	// CREATE THE CLUSTER STATE SNAPSHOT

	fmt.Println("Reading live state from active Kubernetes cluster...")
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "!node-role.kubernetes.io/control-plane",
	})
	if err != nil {
		log.Fatalf("API Error fetching active cluster nodes: %v", err)
	}
	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster pods: %v", err)
	}
	fmt.Printf("Cluster discovery completed. Found %d Nodes and %d Pods.\n", len(nodes.Items), len(pods.Items))

	// Create pointer slices for the Autoscaler's SetClusterState function.
	nodesPointers := make([]*apiv1.Node, len(nodes.Items))
	for i := range nodes.Items {
		nodesPointers[i] = &nodes.Items[i]
	}
	podsPointers := make([]*apiv1.Pod, len(pods.Items))
	for i := range pods.Items {
		podsPointers[i] = &pods.Items[i]
	}

	snapshotStore := store.NewDeltaSnapshotStore(PARALLELISM)
	snapshot := predicate.NewPredicateSnapshot(snapshotStore, fwHandle, false, PARALLELISM, false)
	err = snapshot.SetClusterState(nodesPointers, podsPointers, nil, nil)
	if err != nil {
		log.Fatalf("Critical sandbox simulation failure during instantiation: %v", err)
	}

	// DISPLAY THE SNAPSHOT DATA

	fmt.Println("### SNAPSHOT DATA ###")

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatalf("Error listing node infos: %v", err)
	}

	fmt.Printf("\nThe cluster contains the following nodes:\n")
	for _, nodeInfo := range nodeInfos {
		fmt.Printf("- %s\n", nodeInfo.Node().Name)
	}

	fmt.Printf("\n--------------\n")

	fmt.Printf("\nThe pods assigned to each node are:\n")
	for _, nodeInfo := range nodeInfos {
		node := nodeInfo.Node()
		nodePodsInfos := nodeInfo.GetPods()

		fmt.Printf("\n[Node] %s\n", node.Name)
		for _, podInfo := range nodePodsInfos {
			pod := podInfo.GetPod()
			fmt.Printf("    - [Pod] %s/%s\n", pod.Namespace, pod.Name)
		}
	}

	fmt.Printf("\n--------------\n")

	// Selecting candidate nodes for rescheduling.

	sort.Slice(nodeInfos, func(i, j int) bool {
		return nodeInfos[i].GetRequested().GetMilliCPU() < nodeInfos[j].GetRequested().GetMilliCPU()
	})

	candidateNodes := nodeInfos[:CANDIDATE_NODES_NUMBER]

	// Evict pods from candidate nodes
	fmt.Printf("\nSelected %d Least-Allocated Nodes for pods consolidation:\n", CANDIDATE_NODES_NUMBER)
	for _, ni := range candidateNodes {
		fmt.Printf(" -> Node: %s (Current Pods: %d)\n", ni.Node().Name, len(ni.GetPods()))
	}

	var evictedPods []*apiv1.Pod
	for _, ni := range candidateNodes {
		nodeName := ni.Node().Name
		// Copy the pod references out before we start deleting them from the underlying map
		var podsOnNode []*apiv1.Pod
		for _, podInfo := range ni.GetPods() {
			podsOnNode = append(podsOnNode, podInfo.GetPod())
		}

		fmt.Printf("\nVirtually evicting %d pods from node %s...\n", len(podsOnNode), nodeName)
		for _, pod := range podsOnNode {
			err := snapshot.UnschedulePod(pod.Namespace, pod.Name, ni.Node().Name)
			if err != nil {
				fmt.Printf("Warning: Failed to virtually evict pod %s/%s: %v\n", pod.Namespace, pod.Name, err)
				continue
			} else {
				fmt.Println("- Evicted Pod:", pod.Name)
			}
			evictedPods = append(evictedPods, pod)
		}
	}

	/*
		// Sort the evicted pods - this is necessary for bin packing during the rescheduling
		sort.Slice(evictedPods, func(i, j int) bool {
			reqI := snapshot.GetPodResourceRequest(evictedPods[i]) // Helper or manual aggregation
			reqJ := snapshot.GetPodResourceRequest(evictedPods[j])
			return reqI.MilliCPU > reqJ.MilliCPU
		})
	*/

	fmt.Printf("\n#### TESTS #####\n")

	// ** TESTS ** //

	simulator := scheduling.NewHintingSimulator()

	fakePod, err := PodFromPath("pods/normal.yaml")
	if err != nil {
		log.Fatalf("Error loading pod: %v", err)
	}

	// TEST: Create and schedule a fake pod on a specific node of the cluster.
	fmt.Println("\n--- TEST #1---")

	fmt.Print("Forking the snapshot... ")
	snapshot.Fork()
	fmt.Print("OK")

	if len(nodeInfos) == 0 {
		log.Fatalf("No nodes available in snapshot to simulate pod injection.")
	}
	targetNodeName := nodeInfos[0].Node().Name

	fmt.Printf("\nSimulating forced placement of fake pod '%s' onto Node '%s'...\n", fakePod.Name, targetNodeName)
	err = snapshot.ForceAddPod(fakePod, targetNodeName)
	if err != nil {
		log.Fatalf("Failed to force add pod to snapshot: %v", err)
	}

	updatedNodeInfo, err := snapshot.NodeInfos().Get(targetNodeName)
	if err == nil {
		fmt.Printf("Verified: Node '%s' now has %d pod(s) running in simulation.\n",
			targetNodeName, len(updatedNodeInfo.GetPods()))
	}

	fmt.Print("Reverting Snapshot to Baseline... ")
	snapshot.Revert()
	fmt.Println("OK.")

	// Verify the pod was cleanly removed by the revert operation
	revertedNodeInfo, err := snapshot.NodeInfos().Get(targetNodeName)
	if err == nil {
		fmt.Printf("Verified after Revert: Node '%s' is back to %d pod(s).\n",
			targetNodeName, len(revertedNodeInfo.GetPods()))
	}

	// TEST: FORK THE SNAPSHOT, ADD A FAKE POD, SCHEDULE IT, CHECK AND REVERT
	fmt.Println("\n--- TEST #2---")

	fmt.Printf("Simulating the scheduling of a normal fake pod '%s' in the cluster...\n", fakePod.Name)

	fmt.Print("Forking the snapshot... ")
	snapshot.Fork()
	fmt.Println("OK")

	statuses, _, err := simulator.TrySchedulePods(snapshot, []*apiv1.Pod{fakePod}, scheduling.ScheduleAnywhere, false)
	if err != nil {
		log.Fatalf("Scheduling simulation failed: %v", err)
	}

	if len(statuses) > 0 && statuses[0].NodeName != "" {
		fmt.Printf("Success! The simulator scheduled the pod onto Node: %s\n", statuses[0].NodeName)
	} else {
		fmt.Println("Simulation Complete: Pod is unschedulable on current cluster capacity.")
	}

	fmt.Print("Reverting Snapshot to Baseline... ")
	snapshot.Revert()
	fmt.Println("OK")

	// TEST: FORK THE SNAPSHOT, ADD A GIANT FAKE POD, SCHEDULE IT, CHECK AND REVERT
	fmt.Println("\n--- TEST #3---")

	fmt.Printf("Simulating the scheduling of giant fake pod '%s' in the cluster...\n", fakePod.Name)

	fmt.Print("Forking the snapshot... ")
	snapshot.Fork()
	fmt.Println("OK")

	giantPod, err := PodFromPath("pods/giant.yaml")
	if err != nil {
		log.Fatalf("Error loading pod: %v", err)
	}

	statuses, _, err = simulator.TrySchedulePods(snapshot, []*apiv1.Pod{giantPod}, scheduling.ScheduleAnywhere, false)
	if err != nil {
		log.Fatalf("Scheduling simulation failed: %v", err)
	}

	if len(statuses) > 0 && statuses[0].NodeName != "" {
		fmt.Printf("Success! The simulator scheduled the pod onto Node: %s\n", statuses[0].NodeName)
	} else {
		fmt.Println("Simulation Complete: Pod is unschedulable on current cluster capacity.")
	}

	fmt.Print("Reverting Snapshot to Baseline... ")
	snapshot.Revert()
	fmt.Println("OK")
}

func PodFromPath(path string) (*apiv1.Pod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading pod file: %v", err)
	}

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), YAML_BUFFER_SIZE)

	var pod apiv1.Pod
	if err := decoder.Decode(&pod); err != nil {
		return nil, fmt.Errorf("error decoding pod yaml: %v", err)
	}

	return &pod, nil
}
