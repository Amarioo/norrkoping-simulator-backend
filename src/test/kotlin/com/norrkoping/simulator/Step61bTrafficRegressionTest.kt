package com.norrkoping.simulator

import kotlin.test.*

/**
 * Step 6.1b runtime regression, run by default. Replays the live service deterministically (seeded
 * RoadTraffic, service tick arithmetic at time scale 20) with the same controlled scenario as the
 * browser test tests/bridge-traffic.spec.ts, and stops as soon as every positive condition is met
 * (bus #359 crosses at ≈683 s; the third car passage is at ≈1,179 s). Every edge transition is checked.
 */
class Step61bTrafficRegressionTest {
    companion object {
        /** Controlled scenario shared with the browser test: 150 compact, 50 estate, 100 trucks, 80 buses. */
        val SCENARIO = TrafficSettings(compact = 150, estate = 50, trucks = 100, flights = false, timeScale = 20.0, buses = 80)
        const val LIMIT_SECONDS = 1600.0
        const val CAR_PASSAGES = 3
    }

    /** ≥1 bus north to Skvallertorget, a bus dwell there, ≥3 normal car passages incl. ≥1 north on Kungsgatan. */
    private fun accepted(t: TrafficTrace) =
        t.crossings("bus").isNotEmpty() && t.dwells.any { it.stop == "Skvallertorget" && it.kind == "bus" } &&
            t.carPassages().size >= CAR_PASSAGES && t.crossings("car").isNotEmpty()

    @Test fun `live traffic uses the restored bus corridor without artificial reversals or truck traps`() {
        val t = TrafficTrace().run(SCENARIO, LIMIT_SECONDS, ::accepted)
        fun list(items: List<String>) = items.take(10).joinToString("\n  ", "\n  ")

        // Behavioural invariants over every transition of every vehicle class.
        val artificial = t.reversals.filter { it.type == "artificial" }
        assertTrue(artificial.isEmpty(), "artificial reversals:" + list(artificial.map(t::describe)))
        assertTrue(t.violations.isEmpty(), "transitions outside the vehicle policy:" + list(t.violations.map(t::describe)))
        val trucks = t.episodes.filter { it.kind == "truck" }
        assertTrue(trucks.isEmpty(), "trucks entered the Bergsbron northbound trap:" + list(trucks.map(t::describe)))
        assertFalse(t.bridgeNorth in t.allowed.getValue("truck"))
        val failed = t.failedEpisodes()
        assertTrue(failed.isEmpty(), "Bergsbron entries that did not continue past the junction:" + list(failed.map(t::describe)))
        val buses = t.episodes.filter { it.kind == "bus" && it.outcome != "open" }
        assertTrue(buses.all { it.outcome == "crossed" }, "bus entries must continue north to Skvallertorget:" + list(buses.map(t::describe)))

        // Positive service: each requirement is met by an individual tracked episode, not an aggregate.
        assertTrue(accepted(t), "within ${LIMIT_SECONDS.toInt()} s: bus crossings ${t.crossings("bus").size}, " +
            "car passages ${t.carPassages().size} (north ${t.crossings("car").size}), " +
            "Skvallertorget bus dwells ${t.dwells.count { it.stop == "Skvallertorget" }}." +
            list(t.episodes.map(t::describe)))
        val bus = t.crossings("bus").first()
        assertTrue(bus.resolved!! - bus.entered < 60, t.describe(bus))
        assertTrue((t.gangfartEntries["bus-north"] ?: 0) > 0, "no bus entered the Kungsgatan gångfartsområde northbound")
        // Legitimate terminal behaviour still works: reversals happen only at mapped road ends or turning facilities.
        assertTrue(t.reversals.any { it.type == "mappedRoadEnd" })
        assertTrue(t.reversals.all { it.type in setOf("mappedRoadEnd", "turnaroundFacility") })
    }

    @Test fun `trace detects an artificial reversal and attributes it`() {
        // 0 -> 1 -> 2 dead branch where node 2 also has a link closed to trucks: the old Step 6 filter
        // kept 1 <-> 2, so a truck had to reverse at 2. With throughRoadEdges no truck edge leads there.
        val nodes = (0..3).map { RoadNode(it.toLong(), it.toDouble(), 0.0) }
        val pairs = listOf(0 to 1, 1 to 0, 1 to 2, 2 to 1, 2 to 3, 3 to 2)
        val edges = pairs.mapIndexed { i, (a, b) -> RoadEdge(a, b, 1.0, .5, .3, i < 4, "T", i.toLong(), false) }
        val net = TransportNetwork(nodes, edges, emptyList(), emptyList())
        val trace = TrafficTrace(net, RoadAccess.NONE)
        trace.onTransition(7, "truck", 2, 3) // 1 -> 2, then 2 -> 1: node 2 has another (closed) exit
        val r = trace.reversals.single()
        assertEquals("artificial", r.type)
        assertTrue(trace.describe(r).contains("truck #7 reversed at node 2"), trace.describe(r))
        // A reversal at a real road end (node 0 has no other exit) is legitimate terminal behaviour.
        trace.onTransition(7, "truck", 1, 0)
        assertEquals("mappedRoadEnd", trace.reversals.last().type)
        // A transition onto an edge outside the policy is attributed to vehicle, edge and way.
        trace.onTransition(8, "truck", 3, 4)
        assertTrue(trace.violations.single().let { it.vehicle == 8 && it.edge == 4 && it.way == 4L })
    }
}
