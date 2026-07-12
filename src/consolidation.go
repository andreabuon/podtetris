package main

import "fmt"

type PodMove struct {
	podName      string
	fromNodeName string
	toNodeName   string
	cost         int
}

func (pm PodMove) String() string {
	return fmt.Sprintf("Pod '%s' has been moved from node '%s' to node '%s'. Move cost %d", pm.podName, pm.fromNodeName, pm.toNodeName, pm.cost)
}
