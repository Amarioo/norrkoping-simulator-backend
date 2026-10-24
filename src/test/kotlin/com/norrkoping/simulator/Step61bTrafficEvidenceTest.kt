package com.norrkoping.simulator

import java.io.File
import kotlin.system.measureNanoTime
import kotlin.test.*
import kotlinx.serialization.json.*

/**
 * Step 6.1b deterministic traffic evidence: the same seeded RoadTraffic run with Step 6.1 policy
 * (RoadAccess.NONE) and the Step 6.1b policy, tracing every edge transition. Opt-in because it runs
 * minutes of simulated traffic: set STEP61B_EVIDENCE=<output json>. STEP61B_SECONDS sets duration.
 */
class Step61bTrafficEvidenceTest {
    private val n = TransportNetwork.bundled
    private val terrain = TerrainService.bundled

    private fun legacy(candidates: List<Int>): Set<Int> {
        val alive = candidates.toMutableSet()
        do {
            val from = alive.map { n.edges[it].from }.toSet()
            val removed = alive.removeAll { n.edges[it].to !in from }
        } while (removed)
        return alive
    }

    private fun static(access: RoadAccess): JsonObject {
        val policies = roadPolicies(n, access)
        val terminals = access.turnaroundNodes(n)
        return buildJsonObject {
            for (kind in listOf("compact", "truck", "bus")) {
                val candidates = policyEdges(n, kind, access)
                val old = legacy(policyEdges(n, kind, RoadAccess.NONE))
                val allowed = policies.getValue(kind)
                val main = largestStrongComponent(n, allowed)
                val ends = old.filter { id -> val e = n.edges[id]; n.outgoing[e.to].orEmpty().all { n.edges[it].to == e.from } }
                put(kind, buildJsonObject {
                    put("candidates", candidates.size); put("retained", allowed.size)
                    put("legacyRetained", old.size); put("removedVsLegacy", (old - allowed).size)
                    put("mainNodes", main.size)
                    put("busStopNodesInMain", n.nodes.indices.count { n.nodes[it].busStop != null && it in main })
                    put("mappedEndEdgesLegacy", ends.size); put("mappedEndEdgesRetained", ends.count { it in allowed })
                    put("turnaroundFacilitiesInMain", terminals.count { it in main })
                })
            }
        }
    }

    private fun run(access: RoadAccess, seconds: Double): JsonObject {
        lateinit var traffic: RoadTraffic
        val initMs = measureNanoTime { traffic = RoadTraffic(n, terrain, access) } / 1e6
        val settings = TrafficSettings(600, 170, 150, false, 1.0, buses = 80)
        val configureMs = measureNanoTime { traffic.configure(settings) } / 1e6
        val terminals = access.turnaroundNodes(n)
        fun edge(from: Long, to: Long, way: Long) = n.edges.indices.single {
            n.edges[it].way == way && n.nodes[n.edges[it].from].id == from && n.nodes[n.edges[it].to].id == to
        }
        val bridgeNorth = edge(286409009L, 286409016L, 26154805L)
        val bridgeSouth = edge(286409016L, 286409009L, 26154805L)
        val gangfart = setOf(26154806L, 1050773509L, 444474485L, 1212234765L)
        val last = HashMap<Int, Int>()
        val kinds = HashMap<Int, String>()
        val reversals = mutableMapOf<String, MutableMap<String, Int>>()
        val reversalSamples = mutableListOf<JsonObject>()
        val visited = mutableMapOf<String, MutableSet<Int>>()
        var transitions = 0
        var truckBridgeNorth = 0; var busBridgeNorth = 0; var busBridgeSouth = 0; var carBridgeNorth = 0
        var busGangfartNorth = 0; var busGangfartSouth = 0; var outsidePolicy = 0
        val dwellEpisodes = mutableMapOf<String, Int>()
        val dwelling = HashMap<Int, String>()
        val allowed = roadPolicies(n, access)
        val steps = (seconds / .05).toInt()
        var tickNs = 0L
        for (step in 0 until steps) {
            val ns = measureNanoTime { traffic.tick(.05, step * .05) }
            if (step >= 600) tickNs += ns // first 30 simulated seconds are JIT warm-up
            for ((id, kind, e) in traffic.edgeStates()) {
                kinds[id] = kind
                visited.getOrPut(kind) { mutableSetOf() }.add(e)
                val prev = last.put(id, e) ?: continue
                if (prev == e) continue
                transitions++
                if (e !in allowed.getValue(kind)) outsidePolicy++
                val a = n.edges[prev]; val b = n.edges[e]
                if (b.from == a.to && b.to == a.from) {
                    val at = a.to
                    val type = when {
                        n.outgoing[at].orEmpty().all { n.edges[it].to == a.from } -> "mappedRoadEnd"
                        at in terminals -> "turnaroundFacility"
                        else -> "artificial"
                    }
                    reversals.getOrPut(kind) { mutableMapOf() }.merge(type, 1, Int::plus)
                    if (type == "artificial" && reversalSamples.size < 20) reversalSamples += buildJsonObject {
                        put("kind", kind); put("node", n.nodes[at].id); put("way", a.way); put("time", step * .05)
                    }
                }
                if (e == bridgeNorth) when (kind) { "truck" -> truckBridgeNorth++; "bus" -> busBridgeNorth++; else -> carBridgeNorth++ }
                if (e == bridgeSouth && kind == "bus") busBridgeSouth++
                if (kind == "bus" && b.way in gangfart) {
                    // Count once per way entry: northbound means decreasing z (scene north).
                    if (n.edges[prev].way != b.way) if (n.nodes[b.to].z < n.nodes[b.from].z) busGangfartNorth++ else busGangfartSouth++
                }
            }
            if (step % 20 == 0) for (v in traffic.views()) {
                val stop = v.stop
                if (stop == null) dwelling.remove(v.id)
                else if (dwelling[v.id] != stop) { dwelling[v.id] = stop; dwellEpisodes.merge(stop, 1, Int::plus) }
            }
        }
        return buildJsonObject {
            put("simulatedSeconds", seconds); put("vehicles", traffic.edgeStates().size)
            put("initialiseMs", initMs); put("configureMs", configureMs); put("tickMs", tickNs / 1e6 / maxOf(1, steps - 600))
            put("transitions", transitions); put("transitionsOutsidePolicy", outsidePolicy)
            put("reversals", buildJsonObject { reversals.toSortedMap().forEach { (k, m) -> put(k, buildJsonObject { m.toSortedMap().forEach { (t, c) -> put(t, c) } }) } })
            put("artificialReversalSamples", JsonArray(reversalSamples))
            put("bergsbron", buildJsonObject {
                put("truckNorthboundEntries", truckBridgeNorth); put("carNorthboundEntries", carBridgeNorth)
                put("busNorthboundEntries", busBridgeNorth); put("busSouthboundEntries", busBridgeSouth)
                put("busGangfartsomradeNorthbound", busGangfartNorth); put("busGangfartsomradeSouthbound", busGangfartSouth)
            })
            put("dwellEpisodes", dwellEpisodes.values.sum()); put("distinctStopNamesServed", dwellEpisodes.size)
            put("skvallertorgetDwells", dwellEpisodes["Skvallertorget"] ?: 0)
            put("klockaretorpetDwells", dwellEpisodes["Klockaretorpets centrum"] ?: 0)
            put("distinctEdgesVisited", buildJsonObject { visited.toSortedMap().forEach { (k, s) -> put(k, s.size) } })
        }
    }

    @Test fun evidence() {
        val out = System.getenv("STEP61B_EVIDENCE") ?: return
        val seconds = System.getenv("STEP61B_SECONDS")?.toDouble() ?: 1800.0
        // Alternate before/after so JIT warm-up does not favour either; first two rounds are warm-up.
        val variants = listOf("before" to RoadAccess.NONE, "after" to RoadAccess.bundled)
        val policySamples = variants.associate { it.first to mutableListOf<Double>() }
        val initSamples = variants.associate { it.first to mutableListOf<Double>() }
        for (round in 0 until 9) for ((label, access) in if (round % 2 == 0) variants else variants.reversed()) {
            val p = measureNanoTime { roadPolicies(n, access) } / 1e6
            val i = measureNanoTime { RoadTraffic(n, terrain, access) } / 1e6
            if (round >= 2) { policySamples.getValue(label).add(p); initSamples.getValue(label).add(i) }
        }
        fun median(v: List<Double>) = v.sorted()[v.size / 2]
        val policyMs = buildJsonObject {
            for ((label, _) in variants) put(label, buildJsonObject {
                put("policyComputationMs", median(policySamples.getValue(label)))
                put("roadTrafficInitialiseMs", median(initSamples.getValue(label)))
                put("rounds", policySamples.getValue(label).size)
            })
        }
        val report = buildJsonObject {
            put("note", "Deterministic RoadTraffic seed 1309; before = Step 6.1 policy, after = Step 6.1b. Different allowed sets change random spawns, so counts are behaviour evidence, not paired throughput.")
            put("static", buildJsonObject { put("before", static(RoadAccess.NONE)); put("after", static(RoadAccess.bundled)) })
            put("startupMedians", policyMs)
            // STEP61B_REVERSE runs "after" first, to separate order/JIT effects from policy effects.
            val order = if (System.getenv("STEP61B_REVERSE") != null) variants.reversed() else variants
            put("order", JsonArray(order.map { JsonPrimitive(it.first) }))
            for ((label, access) in order) put(label, run(access, seconds))
        }
        File(out).apply { parentFile?.mkdirs() }.writeText(Json { prettyPrint = true }.encodeToString(JsonObject.serializer(), report) + "\n")
    }
}
