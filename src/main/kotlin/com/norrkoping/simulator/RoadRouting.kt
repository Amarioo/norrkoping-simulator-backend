package com.norrkoping.simulator

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * One bus access correction to the importer's class heuristic, keyed by OSM way. `basis` is
 * `osm-tag` / `osm-tag-deny` (explicit bus/psv tags) or `verified-service` (way used by a bus service
 * verified against current operator data).
 */
@Serializable data class AccessRecord(val way: Long, val bus: Boolean, val basis: String, val services: List<String> = emptyList())

/** A mapped turnaround facility (OSM highway=turning_circle|turning_loop node). */
@Serializable data class Turnaround(val node: Long, val highway: String = "turning_circle")

/**
 * Data-driven road policy inputs for the bundled graph, with provenance in road-access.json (generated
 * by the UI's scripts/step61b/build-road-access.mjs). Neither part changes graph geometry or topology,
 * and entries for ways/nodes absent from a network have no effect.
 */
@Serializable data class RoadAccess(val records: List<AccessRecord> = emptyList(), val turnarounds: List<Turnaround> = emptyList()) {
    private val bus by lazy { records.associate { it.way to it.bus } }
    fun bus(edge: RoadEdge): Boolean = bus[edge.way] ?: edge.bus
    /** Indices of [network] nodes where reversing is a lawful turning movement. */
    fun turnaroundNodes(network: TransportNetwork): Set<Int> {
        val wanted = turnarounds.mapTo(HashSet()) { it.node }
        return network.nodes.indices.filterTo(HashSet()) { network.nodes[it].id in wanted }
    }
    companion object {
        val NONE = RoadAccess()
        private val codec = Json { ignoreUnknownKeys = true }
        val bundled by lazy {
            checkNotNull(RoadAccess::class.java.getResourceAsStream("/road-access.json")).bufferedReader()
                .use { codec.decodeFromString<RoadAccess>(it.readText()) }
        }
    }
}

/**
 * Edges a vehicle class may traverse: the importer's truck/bus flags, with bus flags corrected by
 * [access]. Cars use every drivable edge. Through-route pruning ([throughRoadEdges]) runs on this set.
 */
internal fun policyEdges(network: TransportNetwork, kind: String, access: RoadAccess): List<Int> =
    network.edges.indices.filter {
        val e = network.edges[it]
        when (kind) { "truck" -> e.truck; "bus" -> access.bus(e); else -> true }
    }

/**
 * Edges from which roaming traffic can continue without a policy-induced U-turn.
 * A bidirectional branch cut off by truck/bus filters is NOT a usable directed cycle. Pruning nodes
 * with zero outgoing edges alone misses it. Legitimate termini keep their turnaround behavior: a real
 * mapped road end (no other target in the full graph) and a mapped turnaround facility in [terminals]
 * (a driveway beyond a turning circle does not make reversing there artificial). Elsewhere, an onward
 * link present in the full graph but unavailable to this policy is a trap and is propagated back.
 * Counts are per destination (parallel OSM ways must not masquerade as different exits).
 * O(nodes + edges), once per vehicle policy; no graph search in tick/fillPath.
 */
internal fun throughRoadEdges(network: TransportNetwork, candidates: Collection<Int>, terminals: Set<Int> = emptySet()): Set<Int> {
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
        // A true road end or turnaround facility is a legitimate turnaround. At a policy boundary, or a
        // branch leading to one, the full map has another exit: propagate its unavailability back.
        if (only != null && (node in terminals || network.outgoing[node].orEmpty().all { network.edges[it].to == only })) return
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

/** Allowed edges for every road vehicle class: access policy, then through-route pruning. */
internal fun roadPolicies(network: TransportNetwork, access: RoadAccess): Map<String, Set<Int>> {
    val terminals = access.turnaroundNodes(network)
    return listOf("compact", "estate", "truck", "bus").associateWith { kind ->
        val candidates = policyEdges(network, kind, access)
        // Tiny standalone/test networks can have no through route at all; retain their finite-route
        // behavior. The bundled city has thousands of through edges for every supported kind.
        throughRoadEdges(network, candidates, terminals).ifEmpty { candidates.toSet() }
    }
}
