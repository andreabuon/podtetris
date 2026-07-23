package main

import (
	"context"
	"log"
	"time"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

const controlPlaneLabelSelector = "!node-role.kubernetes.io/control-plane"

func initInformerFactory(clientset *kubernetes.Clientset) informers.SharedInformerFactory {
	if clientset == nil {
		log.Fatal("No clientset provided to initInformerFactory")
	}

	informerFactory := informers.NewSharedInformerFactory(clientset, 30*time.Second)

	nodeInformer := informerFactory.Core().V1().Nodes()
	podInformer := informerFactory.Core().V1().Pods()
	pvInformer := informerFactory.Core().V1().PersistentVolumes()
	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims()
	scsInformer := informerFactory.Storage().V1().StorageClasses()

	_ = nodeInformer.Informer()
	_ = podInformer.Informer()
	_ = pvInformer.Informer()
	_ = pvcInformer.Informer()
	_ = scsInformer.Informer()

	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)
	informerFactory.WaitForCacheSync(stopCh)

	// # Test
	pvcs, err := pvcInformer.Lister().List(labels.Everything())
	log.Printf("PVC lister: %d items, err=%v", len(pvcs), err)
	pvs, err := pvInformer.Lister().List(labels.Everything())
	log.Printf("PV lister: %d items, err=%v", len(pvs), err)
	scs, err := scsInformer.Lister().List(labels.Everything())
	log.Printf("StorageClass lister: %d items, err=%v", len(scs), err)

	return informerFactory
}

func fetchClusterState(ctx context.Context, clientset *kubernetes.Clientset) ([]*apiv1.Node, []*apiv1.Pod) {
	if clientset == nil {
		log.Fatal("No clientset provided to fetchClusterState")
	}

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster nodes: %v", err)
	}

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Fatalf("API Error fetching active cluster pods: %v", err)
	}

	nodePointers := make([]*apiv1.Node, len(nodes.Items))
	for i := range nodes.Items {
		nodePointers[i] = &nodes.Items[i]
	}

	podPointers := make([]*apiv1.Pod, len(pods.Items))
	for i := range pods.Items {
		podPointers[i] = &pods.Items[i]
	}

	return nodePointers, podPointers
}
