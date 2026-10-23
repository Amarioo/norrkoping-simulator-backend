package com.norrkoping.simulator

import java.io.File
import kotlinx.serialization.json.*
import kotlin.test.*

/** Bounded audit of the existing filter; no routing policy changes. Report stays under build/. */
class Step62RoutingAuditTest {
    private fun legacy(n: TransportNetwork, candidates: List<Int>): Set<Int> {
        val alive = candidates.toMutableSet()
        do {
            val from = alive.map { n.edges[it].from }.toSet()
            val removed = alive.removeAll { n.edges[it].to !in from }
        } while (removed)
        return alive
    }

    @Test fun `audit policy reachability stops route 51 and actual road ends`() {
        val n = TransportNetwork.bundled
        val reports = buildJsonObject {
            for (kind in listOf("compact", "truck", "bus")) {
                val candidates = n.edges.indices.filter {
                    (kind != "truck" || n.edges[it].truck) && (kind != "bus" || n.edges[it].bus)
                }
                val before = legacy(n, candidates)
                val after = throughRoadEdges(n, candidates)
                val removed = before - after
                val realEnds = before.filter { id ->
                    val e = n.edges[id]
                    n.outgoing[e.to].orEmpty().all { n.edges[it].to == e.from }
                }.toSet()
                val lostRealEnds = realEnds - after
                for (id in after) {
                    val e = n.edges[id]
                    assertTrue(id in realEnds || n.outgoing[e.to].orEmpty().any {
                        it in after && n.edges[it].to != e.from
                    }, "$kind retained a forced policy reversal")
                }
                fun reachable(edges: Set<Int>): Set<Int> {
                    val seen = mutableSetOf<Int>()
                    val queue = ArrayDeque<Int>()
                    edges.filter { n.edges[it].length > .7 && n.outgoing[n.edges[it].to].orEmpty().isNotEmpty() }
                        .forEach { if (seen.add(n.edges[it].from)) queue.add(n.edges[it].from) }
                    while (queue.isNotEmpty()) {
                        n.outgoing[queue.removeFirst()].orEmpty().filter { it in edges }.forEach {
                            val to = n.edges[it].to
                            if (seen.add(to)) queue.add(to)
                        }
                    }
                    return seen
                }
                val reachableBefore = reachable(before)
                val reachableAfter = reachable(after)
                val lostStops = n.nodes.indices.filter {
                    n.nodes[it].busStop != null && it in reachableBefore && it !in reachableAfter
                }
                put(kind, buildJsonObject {
                    put("candidates", candidates.size); put("legacyRetained", before.size)
                    put("retained", after.size); put("additionalRemoved", removed.size)
                    put("genuineEndEdgesBefore", realEnds.size)
                    put("genuineEndEdgesPreserved", (realEnds intersect after).size)
                    put("lostGenuineEndEdges", buildJsonArray { lostRealEnds.sorted().forEach { id ->
                        val e = n.edges[id]
                        add(buildJsonObject { put("edge", id); put("way", e.way); put("node", n.nodes[e.to].id) })
                    } })
                    put("reachableNodesBefore", reachableBefore.size); put("reachableNodesAfter", reachableAfter.size)
                    put("busStopNodesBefore", n.nodes.indices.count { n.nodes[it].busStop != null && it in reachableBefore })
                    put("busStopNodesAfter", n.nodes.indices.count { n.nodes[it].busStop != null && it in reachableAfter })
                    put("lostStopNodes", buildJsonArray { lostStops.forEach { i -> add(buildJsonObject {
                        put("id", n.nodes[i].id); put("name", n.nodes[i].busStop)
                    }) } })
                    put("way318001968", buildJsonObject {
                        val ids = n.edges.indices.filter { n.edges[it].way == 318001968L }
                        assertEquals(6, ids.size)
                        put("edges", ids.size); put("candidates", ids.count { it in candidates })
                        put("before", ids.count { it in before }); put("after", ids.count { it in after })
                        put("nodes", buildJsonArray {
                            ids.flatMap { listOf(n.edges[it].from, n.edges[it].to) }.distinct().forEach { i ->
                                add(buildJsonObject {
                                    put("id", n.nodes[i].id)
                                    put("outgoing", buildJsonArray { n.outgoing[i].orEmpty().forEach { id ->
                                        val e = n.edges[id]
                                        add(buildJsonObject {
                                            put("way", e.way); put("to", n.nodes[e.to].id)
                                            put("bus", e.bus); put("truck", e.truck)
                                        })
                                    } })
                                })
                            }
                        })
                    })
                    put("removedWays", buildJsonArray { removed.map { n.edges[it].way }.distinct().sorted().forEach { add(it) } })
                })
            }
            put("designatedRoutes", buildJsonArray { n.transitRoutes.forEach { r -> add(buildJsonObject {
                put("id", r.id); put("kind", r.kind); put("points", r.points.size); put("stops", r.stops.size)
            }) } })
        }
        File("build/step62").mkdirs()
        File("build/step62/routing-audit.json").writeText(reports.toString() + "\n")
        // The bundled transit routes are rail paths, independent of RoadTraffic.allowed.
        assertEquals(setOf("tram-2", "tram-3", "pendeln"), n.transitRoutes.map { it.id }.toSet())
    }
}
