package com.norrkoping.simulator

import java.util.Locale

/**
 * Test-side replay of the live road simulation with an exact trace of every edge transition
 * (RoadTraffic.transitionListener; per-tick sampling can skip edges shorter than one tick's travel).
 *
 * The schedule mirrors CitySimulationService after Configure + Reset: a fresh seeded RoadTraffic,
 * configure(settings), then 0.05 s ticks with time advanced before each tick. Pausing only inserts
 * wall-clock gaps, so a browser run with the same settings replays the same vehicle trajectories.
 */
internal class TrafficTrace(
    private val n: TransportNetwork = TransportNetwork.bundled,
    private val access: RoadAccess = RoadAccess.bundled,
) {
    private fun fmt(t: Double) = String.format(Locale.ROOT, "%.2f", t)
    // Bergsbron lookups return -1 on networks without it (synthetic fixtures).
    private fun node(id: Long) = n.nodes.indices.firstOrNull { n.nodes[it].id == id } ?: -1
    private fun edge(from: Long, to: Long, way: Long) = n.edges.indices.firstOrNull {
        n.edges[it].way == way && n.nodes[n.edges[it].from].id == from && n.nodes[n.edges[it].to].id == to
    } ?: -1

    data class Reversal(val vehicle: Int, val kind: String, val node: Long, val way: Long, val fromEdge: Int, val toEdge: Int, val time: Double, val type: String)
    data class Violation(val vehicle: Int, val kind: String, val edge: Int, val way: Long, val time: Double)
    /** One northbound entry onto Bergsbron by one vehicle, resolved by that same vehicle only. */
    data class Episode(val vehicle: Int, val kind: String, val entered: Double, var outcome: String = "open", var resolved: Double? = null, var detail: String = "")
    data class Dwell(val vehicle: Int, val kind: String, val stop: String, val time: Double)

    val allowed = roadPolicies(n, access)
    private val terminals = access.turnaroundNodes(n)
    val bridgeNorth = edge(286409009L, 286409016L, 26154805L)
    private val junction = node(1108233121L) // Bredgatan junction, north of the gångfartsområde
    private val northOfJunction = node(298337070L) // next Kungsgatan node towards Skvallertorget
    /** Node chain bridge south end → Bredgatan junction (Kungsgatan). */
    private val corridor = listOf(286409009L, 286409016L, 120452L, 3286754732L, 3015112537L, 1108233121L).map(::node).toSet()
    private val gangfart = setOf(26154806L, 1050773509L, 444474485L, 1212234765L)

    var time = 0.0; private set
    var transitions = 0; private set
    val reversals = mutableListOf<Reversal>()
    val violations = mutableListOf<Violation>()
    val episodes = mutableListOf<Episode>()
    private val open = HashMap<Int, Episode>()
    val dwells = mutableListOf<Dwell>()
    private val dwelling = HashMap<Int, String>()
    /** Way entries onto the four gångfartsområde ways by direction ("north"/"south") per kind. */
    val gangfartEntries = mutableMapOf<String, Int>()
    val visited = mutableMapOf<String, MutableSet<Int>>()

    fun describe(r: Reversal) = "${r.kind} #${r.vehicle} reversed at node ${r.node} (way ${r.way}, edge ${r.fromEdge} -> ${r.toEdge}) at t=${fmt(r.time)} s [${r.type}]"
    fun describe(v: Violation) = "${v.kind} #${v.vehicle} entered edge ${v.edge} (way ${v.way}) outside its policy at t=${fmt(v.time)} s"
    fun describe(e: Episode) = "${e.kind} #${e.vehicle} entered Bergsbron northbound at t=${fmt(e.entered)} s: ${e.outcome}" +
        (e.resolved?.let { " at t=${fmt(it)} s" } ?: "") + (if (e.detail.isNotEmpty()) " (${e.detail})" else "")

    /** Transitions that per-tick edge sampling would not have seen (a second edge change in one tick). */
    var hiddenFromSampling = 0; private set
    private val lastTick = HashMap<Int, Int>()
    private var tickIndex = 0

    internal fun onTransition(vehicle: Int, kind: String, fromEdge: Int, toEdge: Int) {
        transitions++
        if (lastTick.put(vehicle, tickIndex) == tickIndex) hiddenFromSampling++
        visited.getOrPut(kind) { mutableSetOf() }.add(toEdge)
        val a = n.edges[fromEdge]; val b = n.edges[toEdge]
        if (toEdge !in allowed.getValue(kind)) violations += Violation(vehicle, kind, toEdge, b.way, time)
        val reversed = b.from == a.to && b.to == a.from
        if (reversed) {
            val at = a.to
            val type = when {
                n.outgoing[at].orEmpty().all { n.edges[it].to == a.from } -> "mappedRoadEnd"
                at in terminals -> "turnaroundFacility"
                else -> "artificial"
            }
            reversals += Reversal(vehicle, kind, n.nodes[at].id, a.way, fromEdge, toEdge, time, type)
        }
        if (b.way in gangfart && a.way != b.way) {
            val dir = if (n.nodes[b.to].z < n.nodes[b.from].z) "north" else "south"
            gangfartEntries.merge("$kind-$dir", 1, Int::plus)
        }
        open[vehicle]?.let { ep ->
            when {
                reversed -> { ep.outcome = "reversed"; ep.detail = "at node ${n.nodes[a.to].id}" }
                b.from == junction && b.to == northOfJunction -> { ep.outcome = "crossed"; ep.detail = "continued north on way ${b.way}" }
                // Leaving onto Bredgatan is a legitimate departure, but not a crossing towards Skvallertorget.
                b.from == junction && b.to !in corridor -> { ep.outcome = "exited at junction"; ep.detail = "way ${b.way}" }
                b.from == junction -> { ep.outcome = "turned back at junction"; ep.detail = "way ${b.way}" }
                b.to !in corridor -> { ep.outcome = "diverted before junction"; ep.detail = "way ${b.way} towards node ${n.nodes[b.to].id}" }
                else -> return@let
            }
            ep.resolved = time
            open.remove(vehicle)
        }
        if (toEdge == bridgeNorth) Episode(vehicle, kind, time).also { episodes += it; open[vehicle] = it }
    }

    /**
     * Replay up to [seconds] of simulated time using CitySimulationService.step(0.1) arithmetic for
     * settings.timeScale; stops are sampled once per step (the live snapshot rate). [until] can end early.
     */
    fun run(settings: TrafficSettings, seconds: Double, until: (TrafficTrace) -> Boolean = { false }): TrafficTrace {
        val traffic = RoadTraffic(n, TerrainService.bundled, access)
        traffic.configure(settings)
        traffic.transitionListener = { id, kind, from, to -> onTransition(id, kind, from, to) }
        val kinds = traffic.edgeStates().associate { it.first to it.second }
        while (time < seconds) {
            var remaining = .1 * settings.timeScale
            while (remaining > .000001) {
                val tick = minOf(.05, remaining)
                time += tick
                tickIndex++
                traffic.tick(tick, time)
                remaining -= tick
            }
            for (v in traffic.views()) {
                val stop = v.stop
                if (stop == null) dwelling.remove(v.id)
                else if (dwelling[v.id] != stop) { dwelling[v.id] = stop; dwells += Dwell(v.id, kinds.getValue(v.id), stop, time) }
            }
            if (until(this)) break
        }
        return this
    }

    /** Episodes that did not continue past the junction: reversed, turned back or diverted before it. */
    fun failedEpisodes() = episodes.filter { it.outcome !in setOf("crossed", "exited at junction", "open") }

    /** Car entries that left past the junction normally (north on Kungsgatan or onto Bredgatan). */
    fun carPassages() = episodes.filter { it.kind in setOf("compact", "estate") && it.outcome in setOf("crossed", "exited at junction") }

    fun crossings(kind: String? = null) = episodes.filter { it.outcome == "crossed" && (kind == null || it.kind == kind || (kind == "car" && it.kind in setOf("compact", "estate"))) }
}
