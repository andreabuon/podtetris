// Note: This pseudocode is a VERY high-level representation and may require adjustments based on the actual Kubernetes API and the specific requirements of the consolidation process.
const kwok = require('./kwok.js');
const { Allocation } = require('./allocation.js');
const simulator = require('./simulator.js');
const utils = require('./utils.js');

const NODES_TO_CONSOLIDATE_NUM = 10;

const UNDERUTILIZED_CPU_THRESHOLD = 0.6;
const UNDERUTILIZED_MEMORY_THRESHOLD = 0.6;

const cluster = "cluster";

// Pseudocode for a Kubernetes cluster consolidation algorithm.
// The algorithm:
// - identifies underutilized nodes in the cluster
// - simulates the rescheduling of the pods present on the identified nodes onto the fake nodes
// - computes the necessary moves to consolidate pods onto fewer nodes and the number of nodes that can be turned off
// - executes the moves to consolidate the cluster to the .
//

// This code runs every x (configurable) hours or after a certain configurable set of events.

function main() {
    // Get a subset of nodes to consolidate
    let consolidatableNodes = cluster.getNodes()
        .filter(node => utils.isConsolidable(node) )
        .sortByUsage()
        .take(NODES_TO_CONSOLIDATE_NUM);

    // Save the current allocation state of the cluster nodes to consolidate.
    let currentAllocation = new Allocation();

    let movablePods = []; // change data structure for better performance (rank pods by resource usage when inserting them in the list?)
    let fixedPods = []; // change data structure for better performance (rank pods by resource usage when inserting them in the list?)

    for (let node of consolidatableNodes) {
        let pods = node.getPods(); // what about Deployments, DaemonSets, StatefulSets, etc.?
        
        for (let pod of pods) {
            currentAllocation.addPodLocation(pod, node);

            if (utils.isMovable(pod)) {
                movablePods.push(pod);
                continue;
            }
            else
                fixedPods.push(pod);
        }
    }

    // Sort movable pods by resource usage to try to pack them more efficiently on the fake nodes during the simulation.
    let podsOrder1 = movablePods.sort((a, b) => a.cpu - b.cpu);
    //TODO: compute different ordering of pods?

    let newAllocation = new Allocation()

    // Re-assign the fixed pods to the same nodes they are currently allocated to, to ensure they are not moved during the consolidation process.
    for (let fixedPod of fixedPods) {
        newAllocation.addPodLocation(fixedPod, currentAllocation.getPodLocation(fixedPod));
    }

    // Simulate the new scheduling of movable pods
    simulator.simulateScheduling(podsOrder1, consolidatableNodes, newAllocation);
    
    // Compare the new pods allocations with the current one.
    // If consolidation is possible, consolidate the cluster.
    if (newAllocation.getRequiredNodes() >= consolidatableNodes.length) {
        console.log("No consolidation possible at this time.");
        return;
    }

    let moves = newAllocation.computeMovesFrom(currentAllocation);
    for (let move of moves) {
        cluster.movePod(move.pod, move.targetNode); // how to do this?
    }

    console.log("Consolidation completed successfully.");
    return;
}