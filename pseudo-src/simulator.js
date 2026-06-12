const kwok = require ("kwok")

export class SimulationResult {
    constructor (freeNodes, movesNum, moves){
        this.freeNodes = freeNodes
        this.movesNum = movesNum
        this.moves = moves
    }
}

export class PodMovement {
    constructor(pod, prevNode, newNode){
        this.pod = pod
        this.prevNode = prevNode
        this.newNode
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

function simulateScheduling(podsList, nodes, allocation) {
    let fakeNodes = createFakeNodesFrom(nodes);

    // Duplicate movable pods and simulate their scheduling on the fake nodes.
    let fakePods = duplicatePods(podsList);
    schedulePods(fakePods, fakeNodes);
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