// Pseudocode for a Kubernetes cluster consolidation algorithm.

// Note: This pseudocode is a VERY high-level representation.
// It may require adjustments based on the actual Kubernetes API and the specific requirements of the consolidation process.

// This code runs every x (configurable) hours or after a certain configurable set of events.

// The algorithm:
// - identifies a subset of nodes in the cluster
// - simulates the rescheduling of the pods present on the identified nodes onto the fake nodes to check if node consolidation is possible.
// - computes the necessary moves to consolidate pods onto fewer nodes and the number of nodes that can be turned off
// - executes the moves to consolidate the cluster to the desired state.

const { Allocation } = require('./allocation.js');
const simulator = require('./simulator.js');
const utils = require('./utils.js');

const NODES_TO_CONSOLIDATE_NUM = 10;
const UNDERUTILIZED_CPU_THRESHOLD = 0.6;
const UNDERUTILIZED_MEMORY_THRESHOLD = 0.6;
const CLUSTER = "cluster";

let PREFERRED_METRIC = 'freedNodesCount'

function main() {
    let consolidatableNodes = CLUSTER.getNodes()
        .filter(node => utils.isConsolidatable(node) )
        .sortByUsage()
        .take(NODES_TO_CONSOLIDATE_NUM);

    // Save the current allocation state of the cluster.
    let currentAllocation = Allocation.fromClusterNodes(consolidatableNodes);
    let currentNodesCount = consolidatableNodes.length
    let fixedPods = currentAllocation.getFixedPods();

    // ** RE-SCHEDULING ** //
    // Re-assign the fixed pods to the same nodes they are currently allocated to. 
    let fixedAllocation = new Allocation();
    for (let fixedPod of fixedPods) {
        fixedAllocation.addPodLocation(fixedPod, currentAllocation.getPodLocation(fixedPod));
    }

    let movablePods = currentAllocation.getMovablePods();
    let sortingAlgorithms = [byCPUusage, byMemoryUsage]
    let results = [];
    
    // Reschedule the same set of pods many times (using different pods orderings) and save the results.
    for(let algorithm of sortingAlgorithms){
        // Sort movable pods by resource usage to try to pack them more efficiently on the fake nodes during the simulation.
        let podsOrder = movablePods.sort(algorithm);
        
        let newAllocation = structuredClone(fixedAllocation);
        simulator.simulateScheduling(podsOrder, newAllocation);
        
        let newNodesCount = newAllocation.getRequiredNodesNum();
        let freedNodesCount = currentNodesCount - newNodesCount;
        if ( freedNodesCount <= 0 ) {
            console.log("No consolidation is possible with the algorithm: " + algorithm.name);
            continue;
        }

        let moves = newAllocation.computeMovesFrom(currentAllocation);

        results.push(new simulator.SimulationResult(freedNodesCount, moves))
    }

    if(results.length == 0){
        console.log("No consolidation is possible with any algorithm.");
        return;
    }
    
    bestSimulationResult = results.sortBy(PREFERRED_METRIC).take(1)
    for (let move of bestSimulationResult.moves) {
            CLUSTER.perform(move); // how to implement this? 
    }

    console.log("Consolidation completed successfully.");
    return;
}