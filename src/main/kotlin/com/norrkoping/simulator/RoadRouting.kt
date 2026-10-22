package com.norrkoping.simulator

/**
 * Edges from which roaming traffic can continue without a policy-induced U-turn.
 * A bidirectional branch cut off by truck/bus filters is NOT a usable directed cycle. Pruning nodes
 * with zero outgoing edges alone misses it. Real mapped cul-de-sacs retain their existing turnaround
 * behavior: only an onward link present in the full graph but unavailable to this policy is a trap.
 * Counts are per destination (parallel OSM ways must not masquerade as different exits).
 * O(nodes + edges), once per vehicle policy; no graph search in tick/fillPath.
 */
internal fun throughRoadEdges(network: TransportNetwork, candidates: Collection<Int>): Set<Int> {
    val exits = Array(network.nodes.size) { mutableMapOf<Int, Int>() }
    val incoming = Array(network.nodes.size) { mutableListOf<Int>() }
    val alive = candidates.toMutableSet()
    for (id in candidates) {
        val e = network.edges[id]
        exits[e.from][e.to] = (exits[e.from][e.to] ?: 0) + 1
        incoming[e.to].add(id)
    }
    val queue = ArrayDeque<Int>()
    fun inspect(node: Int) {
        val targets = exits[node]
        if (targets.size > 1) return
        val only = targets.keys.firstOrNull()
        // A true road end is a legitimate turnaround. At a policy boundary, or a branch leading to
        // one, the full map has another exit: propagate its unavailability back towards the junction.
        if (only != null && network.outgoing[node].orEmpty().all { network.edges[it].to == only }) return
        for (id in incoming[node])
            if (only == null || network.edges[id].from == only) queue.addLast(id)
    }
    network.nodes.indices.forEach(::inspect)
    while (queue.isNotEmpty()) {
        val id = queue.removeFirst()
        if (!alive.remove(id)) continue
        val e = network.edges[id]
        val targets = exits[e.from]
        val count = targets.getValue(e.to) - 1
        if (count == 0) {
            targets.remove(e.to)
            inspect(e.from)
        } else targets[e.to] = count
    }
    return alive
}
