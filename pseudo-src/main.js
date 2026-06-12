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
const kwok = require('./kwok.js');
const simulator = require('./simulator.js');
const utils = require('./utils.js');

const NODES_TO_CONSOLIDATE_NUM = 10;
const UNDERUTILIZED_CPU_THRESHOLD = 0.6;
const UNDERUTILIZED_MEMORY_THRESHOLD = 0.6;
const CLUSTER = "cluster";

function main() {
    // Get a subset of nodes to consolidate
    let consolidatableNodes = CLUSTER.getNodes()
        .filter(node => utils.isConsolidatable(node) )
        .sortByUsage()
        .take(NODES_TO_CONSOLIDATE_NUM);

    // Save the current allocation state of the cluster.
    let currentAllocation = Allocation.fromClusterNodes(consolidatableNodes);
    let fixedPods = currentAllocation.getFixedPods();
    let movablePods = currentAllocation.getMovablePods();

    // Sort movable pods by resource usage to try to pack them more efficiently on the fake nodes during the simulation.
    let podsOrder1 = movablePods.sort((a, b) => a.cpu - b.cpu);
    //TODO: compute different ordering of pods?

    // ** RE-SCHEDULING ** //
    let newAllocation = new Allocation();

    // Re-assign the fixed pods to the same nodes they are currently allocated to,
    // This is to ensure they are not moved during the consolidation process.
    for (let fixedPod of fixedPods) {
        newAllocation.addPodLocation(fixedPod, currentAllocation.getPodLocation(fixedPod));
    }

    simulator.simulateScheduling(podsOrder1, consolidatableNodes, newAllocation);
    
    if (newAllocation.getRequiredNodesNum() >= consolidatableNodes.length) {
        console.log("No consolidation possible at this time.");
        return;
    }

    let moves = newAllocation.computeMovesFrom(currentAllocation);
    for (let move of moves) {
        CLUSTER.movePod(move.pod, move.targetNode); // how to do this?
    }

    console.log("Consolidation completed successfully.");
    return;
}