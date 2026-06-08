const 

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
        fakePod.setNodeSelector("fakePods");
        fakePods.push(fakePod);
    }
    return fakePods;
}