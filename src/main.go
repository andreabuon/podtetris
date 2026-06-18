package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog/v2"

	"k8s.io/autoscaler/cluster-autoscaler/simulator/clustersnapshot/store"
)

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()

	home := homedir.HomeDir()
	if home == "" {
		log.Fatalf("Critical: Could not locate home directory to read kubeconfig.")
	}
	kubeconfig := filepath.Join(home, ".kube", "config")

	// Build the configuration client context
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		log.Fatalf("Error loading local kubeconfig: %v", err)
	}

	// Create the standard client-go clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating live Kubernetes clientset: %v", err)
	}

	ctx := context.Background()
	fmt.Println("Reading live state from active Kubernetes cluster...")

	// Query active Nodes
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster nodes: %v", err)
	}

	// Query active Pods across ALL namespaces
	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster pods: %v", err)
	}

	fmt.Printf("Cluster discovery completed. Found %d Nodes and %d Pods.\n", len(nodes.Items), len(pods.Items))

	// Convert concrete API slices to the pointer slices (*apiv1.Node and *apiv1.Pod) that the Autoscaler's SetClusterState signature strictly expects.
	var nodePointers []*apiv1.Node
	for i := range nodes.Items {
		nodePointers = append(nodePointers, &nodes.Items[i])
	}

	var podPointers []*apiv1.Pod
	for i := range pods.Items {
		podPointers = append(podPointers, &pods.Items[i])
	}

	// Create the snapshot and populate it with the live cluster data
	snapshot := store.NewDeltaSnapshotStore(1)

	err = snapshot.SetClusterState(nodePointers, podPointers, nil, nil)
	if err != nil {
		log.Fatalf("Critical sandbox simulation failure during instantiation: %v", err)
	}

	nodeInfos, err := snapshot.NodeInfos().List()
	if err != nil {
		log.Fatalf("Error listing node infos: %v", err)
	}

	fmt.Printf("The cluster contains the following nodes:\n")
	for _, nodeInfo := range nodeInfos {
		fmt.Printf("- %s\n", nodeInfo.Node().Name)
	}
	fmt.Printf("\n--------------\n")

	fmt.Printf("The pods assigned to each node are:\n")

	for _, nodeInfo := range nodeInfos {
		node := nodeInfo.Node()
		nodePodsInfos := nodeInfo.GetPods()

		fmt.Printf("\n[Node] %s\n", node.Name)
		for _, podInfo := range nodePodsInfos {
			pod := podInfo.GetPod()
			fmt.Printf("    - [Pod] %s/%s\n", pod.Namespace, pod.Name)
		}
	}
}
