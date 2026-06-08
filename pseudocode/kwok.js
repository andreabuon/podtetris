function createFakeNodesFrom(realNodes) {
    let fakeNodes = [];
    for (let node of realNodes) {
        let fakeNode = kwok.createFakeNode(node);
        fakeNode.setLabel("fakePods", "true");
        fakeNodes.push(fakeNode);
    }
    return fakeNodes;
}