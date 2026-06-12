const kwok = require ("kwok")

export class SimulationResult {
    constructor (freeNodes, moves){
        this.freeNodes = freeNodes
        this.movesNum = moves.length
        this.moves = moves
    }
}

export class PodMovement {
    constructor(pod, prevNode, newNode){
        this.pod = pod
        this.prevNode = prevNode
        this.newNode = newNode
    }
}

function createFakeNodesFrom(realNodes) {
    let fakeNodes = [];
    for (let node of realNodes) {
        let fakeNode = kwok.createFakeNode(node);
        fakeNode.setLabel("fake-node", "true");
        fakeNodes.push(fakeNode);
    }
    return fakeNodes;
}

export function simulateScheduling(podsList, allocation) {
    let fakeNodes = createFakeNodesFrom(allocation.getNodes());

    // Duplicate movable pods and simulate their scheduling on the fake nodes.
    let fakePods = duplicatePods(podsList);
    schedulePods(fakePods, fakeNodes, allocation);
}

function schedulePods(pods, nodes, allocation) {
    for (let pod of pods) {
        let targetNode = submitPodToScheduler(pod, nodes);
        allocation.addAllocation(pod, targetNode);
    }
}

function duplicatePods(realPods) {
    let fakePods = [];
    for (let pod of realPods) {
        let fakePod = kwok.createFakePod(pod);
        fakePod.setNodeSelector("fake-pod", "true");
        fakePods.push(fakePod);
    }
    return fakePods;
}