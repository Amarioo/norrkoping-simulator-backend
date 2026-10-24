package com.norrkoping.simulator

import java.io.File
import kotlin.system.measureNanoTime
import kotlin.test.*
import kotlinx.serialization.json.*

/**
 * OPT-IN long measurement/export run; it does NOT execute in the default `gradlew test`. Set
 * STEP61B_EVIDENCE=<output json> (optional STEP61B_SECONDS, default 3600; STEP61B_REVERSE runs the
 * Step 6.1b policy first). Default runtime coverage is Step61bTrafficRegressionTest.
 *
 * Replays the live service (TrafficTrace: seed 1309, time scale 20, exact transitions) with Step 6.1
 * policy (RoadAccess.NONE) and Step 6.1b policy, writes measurements and ASSERTS the acceptance
 * criteria for Step 6.1b plus the reproduced Step 6.1 regression (so the checks can fail).
 */
class Step61bTrafficEvidenceTest {
    private val n = TransportNetwork.bundled
    private val terrain = TerrainService.bundled
    private val scenario = TrafficSettings(600, 170, 150, false, 20.0, buses = 80)

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

    private fun run(access: RoadAccess, seconds: Double): Pair<TrafficTrace, JsonObject> {
        lateinit var t: TrafficTrace
        val ms = measureNanoTime { t = TrafficTrace(n, access).run(scenario, seconds) } / 1e6
        fun count(kind: String, type: String) = t.reversals.count { it.kind == kind && it.type == type }
        return t to buildJsonObject {
            put("simulatedSeconds", t.time); put("wallMs", ms); put("transitions", t.transitions)
            put("transitionsHiddenFromPerTickSampling", t.hiddenFromSampling)
            put("transitionsOutsidePolicy", t.violations.size)
            put("reversals", buildJsonObject {
                for (kind in listOf("compact", "estate", "truck", "bus")) put(kind, buildJsonObject {
                    for (type in listOf("mappedRoadEnd", "turnaroundFacility", "artificial")) put(type, count(kind, type))
                })
            })
            put("artificialReversals", JsonArray(t.reversals.filter { it.type == "artificial" }.take(20).map { JsonPrimitive(t.describe(it)) }))
            put("bergsbronEpisodes", JsonArray(t.episodes.map { e -> buildJsonObject {
                put("vehicle", e.vehicle); put("kind", e.kind); put("entered", e.entered); put("outcome", e.outcome)
                e.resolved?.let { put("resolved", it) }; put("detail", e.detail)
            } }))
            put("crossings", buildJsonObject { for (k in listOf("car", "truck", "bus")) put(k, t.crossings(k).size); put("carPassagesInclBredgatan", t.carPassages().size) })
            put("gangfartsomradeWayEntries", buildJsonObject { t.gangfartEntries.toSortedMap().forEach { (k, v) -> put(k, v) } })
            put("skvallertorgetBusesServed", t.dwells.filter { it.stop == "Skvallertorget" }.map { it.vehicle }.distinct().size)
            put("klockaretorpetBusesServed", t.dwells.filter { it.stop == "Klockaretorpets centrum" }.map { it.vehicle }.distinct().size)
            put("distinctStopNamesServed", t.dwells.map { it.stop }.distinct().size)
            put("distinctEdgesVisited", buildJsonObject { t.visited.toSortedMap().forEach { (k, s) -> put(k, s.size) } })
        }
    }

    @Test fun evidence() {
        val out = System.getenv("STEP61B_EVIDENCE")
        // Reported as SKIPPED (not passed) in the default suite; see class comment.
        org.junit.jupiter.api.Assumptions.assumeTrue(out != null, "opt-in: set STEP61B_EVIDENCE=<output json>")
        out!!
        val seconds = System.getenv("STEP61B_SECONDS")?.toDouble() ?: 3600.0
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
        val order = if (System.getenv("STEP61B_REVERSE") != null) variants.reversed() else variants
        val runs = order.associate { (label, access) -> label to run(access, seconds) }
        val report = buildJsonObject {
            put("note", "Deterministic replay of the live service (seed 1309, time scale 20, scenario 600/170/150/80, every transition traced). Before = Step 6.1 policy, after = Step 6.1b. Different allowed sets change random spawns, so counts are behaviour evidence, not paired throughput.")
            put("static", buildJsonObject { put("before", static(RoadAccess.NONE)); put("after", static(RoadAccess.bundled)) })
            put("startupMedians", buildJsonObject {
                for ((label, _) in variants) put(label, buildJsonObject {
                    put("policyComputationMs", median(policySamples.getValue(label)))
                    put("roadTrafficInitialiseMs", median(initSamples.getValue(label)))
                    put("rounds", policySamples.getValue(label).size)
                })
            })
            put("order", JsonArray(order.map { JsonPrimitive(it.first) }))
            for ((label, r) in runs) put(label, r.second)
        }
        File(out).apply { parentFile?.mkdirs() }.writeText(Json { prettyPrint = true }.encodeToString(JsonObject.serializer(), report) + "\n")

        // Acceptance assertions (after writing, so the report is available when one fails).
        for ((label, r) in runs) {
            val t = r.first
            val artificial = t.reversals.filter { it.type == "artificial" }
            assertTrue(artificial.isEmpty(), "$label: " + artificial.take(10).joinToString("\n") { t.describe(it) })
            assertTrue(t.violations.isEmpty(), "$label: " + t.violations.take(10).joinToString("\n") { t.describe(it) })
            assertTrue(t.episodes.none { it.kind == "truck" }, "$label: " + t.episodes.filter { it.kind == "truck" }.joinToString("\n") { t.describe(it) })
            assertTrue(t.failedEpisodes().isEmpty(), "$label: " + t.failedEpisodes().joinToString("\n") { t.describe(it) })
            assertTrue(t.carPassages().size >= 3 && t.crossings("car").isNotEmpty(), "$label: car passages ${t.carPassages().size}, north ${t.crossings("car").size}")
        }
        val after = runs.getValue("after").first
        assertTrue(after.crossings("bus").isNotEmpty(), "Step 6.1b: no bus crossed Bergsbron northbound")
        assertTrue((after.gangfartEntries["bus-north"] ?: 0) > 0)
        assertTrue(after.dwells.any { it.stop == "Skvallertorget" })
        val before = runs.getValue("before").first
        assertTrue(before.crossings("bus").isEmpty() && before.dwells.none { it.stop == "Skvallertorget" },
            "Step 6.1 policy should reproduce the lost Skvallertorget service")
    }
}
