function isConsolidatable(node) {
    return true;
    // return node.reservedCPU < UNDERUTILIZED_CPU_THRESHOLD && node.reservedMemory < UNDERUTILIZED_MEMORY_THRESHOLD ;
}

function isMovable(pod) {
    return pod.labels["movable"] == "true";
}