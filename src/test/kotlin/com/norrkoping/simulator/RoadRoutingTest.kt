package com.norrkoping.simulator

import kotlin.test.*

class RoadRoutingTest {
    private fun fixture(pairs: List<Pair<Int, Int>>): TransportNetwork {
        val nodes = (0..pairs.flatMap { listOf(it.first, it.second) }.max()).map {
            RoadNode(it.toLong(), it.toDouble(), (it % 2).toDouble())
        }
        return TransportNetwork(nodes, pairs.map { (a, b) ->
            RoadEdge(a, b, 1.0, .5, .3, true, "Test", 1, true)
        }, emptyList(), emptyList())
    }

    @Test fun `policy-cut spur is pruned back to its junction but exits remain usable`() {
        val n = fixture(listOf(0 to 1, 1 to 2, 2 to 0, 1 to 3, 3 to 1, 3 to 4, 4 to 3, 4 to 5, 5 to 4))
        assertEquals(setOf(0, 1, 2, 4, 6, 8), throughRoadEdges(n, n.edges.indices.filter { it != 7 }))
    }

    @Test fun `real mapped cul-de-sacs preserve existing traffic and turnaround behavior`() {
        val n = fixture(listOf(0 to 1, 1 to 2, 2 to 0, 1 to 3, 3 to 1, 3 to 4, 4 to 3))
        assertEquals(n.edges.indices.toSet(), throughRoadEdges(n, n.edges.indices.toList()))
    }

    @Test fun `parallel reverse ways cannot disguise a dead end`() {
        val n = fixture(listOf(0 to 1, 1 to 2, 2 to 0, 1 to 3, 3 to 1, 3 to 1, 3 to 4))
        assertEquals(setOf(0, 1, 2, 4, 5), throughRoadEdges(n, (0..5).toList()))
    }

    @Test fun `one-way traps are pruned and legitimate loops and one-way exits survive`() {
        val n = fixture(listOf(0 to 1, 1 to 2, 2 to 0, 1 to 3, 3 to 4, 5 to 0))
        assertEquals(setOf(0, 1, 2, 5), throughRoadEdges(n, n.edges.indices.toList()))
    }

    @Test fun `Kungsgatan bridge connects cars through Skvallertorget without a reversal`() {
        val n = TransportNetwork.bundled
        val car = throughRoadEdges(n, n.edges.indices.toList())
        val ids = listOf(286409009L, 286409016L, 120452L, 3286754732L, 3015112537L,
            1108233121L, 298337070L, 12533815721L, 11634055968L, 12533823572L, 11231112376L)
        for ((from, to) in ids.zipWithNext()) {
            val edge = n.edges.indices.single { n.nodes[n.edges[it].from].id == from && n.nodes[n.edges[it].to].id == to }
            assertTrue(edge in car, "Missing northbound connection $from -> $to")
        }
        for (kind in listOf("truck", "bus")) {
            val candidates = n.edges.indices.filter { if (kind == "truck") n.edges[it].truck else n.edges[it].bus }
            val through = throughRoadEdges(n, candidates)
            val northbound = n.edges.indices.single {
                n.edges[it].way == 26154805L && n.nodes[n.edges[it].to].id == 286409016L
            }
            assertTrue(northbound in candidates, "Reproduces the old mode-specific trap")
            assertFalse(northbound in through, "$kind must not enter a bridge with only a forbidden onward road")
            assertTrue(through.size > 1000)
            for (id in through) {
                val e = n.edges[id]
                val realEnd = n.outgoing[e.to].orEmpty().all { n.edges[it].to == e.from }
                assertTrue(realEnd || n.outgoing[e.to].orEmpty().any { it in through && n.edges[it].to != e.from },
                    "$kind edge $id would force a reversal")
            }
        }
    }

    @Test fun `all retained car edges have an onward route or a real mapped road end`() {
        val n = TransportNetwork.bundled
        val through = throughRoadEdges(n, n.edges.indices.toList())
        for (id in through) {
            val e = n.edges[id]
            val realEnd = n.outgoing[e.to].orEmpty().all { n.edges[it].to == e.from }
            assertTrue(realEnd || n.outgoing[e.to].orEmpty().any { it in through && n.edges[it].to != e.from }, "Edge $id")
        }
        // Existing underpass routes must still carry traffic.
        for (way in listOf(39445117L, 89615815L))
            assertTrue(through.any { n.edges[it].way == way })
    }
}
