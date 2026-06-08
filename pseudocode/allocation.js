class Allocation {
    constructor() {        
        this.NodesWorkloads = new Map(); // Map<Node, Set<Pod>>
        this.PodLocations = new Map(); // Map<Pod, Node>
    }

    addPodLocation(pod, node) {
        if (!this.NodesWorkloads.has(node)) {
            this.NodesWorkloads.set(node, new Set());
        }
        this.NodesWorkloads.get(node).add(pod);

        this.PodLocations.set(pod, node);
    };

    getPodLocation(pod) {
        return this.PodLocations.get(pod);
    };

    getNodeWorkloads(node) {
        return this.NodesWorkloads.get(node) || new Set();
    };

    getNodes() {
        return this.NodesWorkloads.keys();
    }

    // This method calculates how many nodes are required to host all the pods in the current allocation. It counts only the nodes that have at least one pod allocated to them.
    getRequiredNodes() {
        return this.getNodes().filter( node => this.getNodeWorkloads(node).size > 0 ).length;
    }

    getAllPods() {
        return this.PodLocations.keys();
    }

    computeMovesFrom(previousAllocation) {
        let moves = [];

        for (let pod of this.getAllPods()) {
            let currentNode = this.getPodLocation(pod);
            let previousNode = previousAllocation.getPodLocation(pod);

            if (currentNode !== previousNode) {
                moves.push({ pod: pod, targetNode: currentNode });
            }
        }

        return moves;
    }
}