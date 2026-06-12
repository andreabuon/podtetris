const { PodMovement } = require('./simulator.js') 

export class Allocation {
    constructor() {     
        this.pods = new Set(); // Set<Pod> // change data structure for better performance (rank pods by resource usage when inserting them in the list?)
        this.nodes = new Set(); // Set<Node> // change data structure for better performance (rank nodes by resource usage when inserting them in the list?)

        this.NodesWorkloads = new Map(); // Map<Node, Set<Pod>>
        this.PodLocations = new Map(); // Map<Pod, Node>
    }

    static fromClusterNodes(nodes) {
        let allocation = new Allocation(nodes);
        for (let node of nodes) {
            allocation.nodes.add(node);

            for (let pod of node.getPods()) {
                allocation.pods.add(pod);
                allocation.addPodLocation(pod, node);
            }
        }
        return allocation;
    }

    addPodLocation(pod, node) {
        if (!this.NodesWorkloads.has(node)) {
            this.NodesWorkloads.set(node, new Set());
        }
        this.NodesWorkloads.get(node).add(pod);

        this.PodLocations.set(pod, node);
    };

    getPods() {
        return this.pods;
    }

    getMovablePods() {
        return Array.from(this.getPods()).filter(pod => pod.isMovable());
    }

    getFixedPods() {
        return Array.from(this.getPods()).filter(pod => !pod.isMovable());
    }

    getNodes() {
        return this.nodes;
    }

    getNodeWorkloads(node) {
        return this.NodesWorkloads.get(node) || new Set();
    };

    getPodLocation(pod) {
        return this.PodLocations.get(pod);
    };

    // This method calculates how many nodes are required to host all the pods in the current allocation. It counts only the nodes that have at least one pod allocated to them.
    getRequiredNodesNum() {
        return Array.from(this.getNodes()).filter( node => this.getNodeWorkloads(node).size > 0 ).length;
    }

    computeMovesFrom(previousAllocation) {
        let moves = [];

        for (let pod of this.getPods()) {
            let newNode = this.getPodLocation(pod);
            let prevNode = previousAllocation.getPodLocation(pod);

            if (newNode !== prevNode) {
                moves.push(new PodMovement(pod, prevNode, newNode));
            }
        }

        return moves;
    }
}