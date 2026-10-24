package com.norrkoping.simulator

import kotlinx.serialization.json.*
import kotlin.test.*

/**
 * Step 6.1b: bus reachability on the bundled network without reintroducing Bergsbron reversals.
 * "Before" is Step 6.1 production (no access record, no turnaround facilities).
 */
class BusReachabilityTest {
    private val n = TransportNetwork.bundled
    private val access = RoadAccess.bundled
    private val after = roadPolicies(n, access)
    private val before = roadPolicies(n, RoadAccess.NONE)
    private val index = n.nodes.indices.associateBy { n.nodes[it].id }
    private fun node(id: Long) = index.getValue(id)
    private fun edge(from: Long, to: Long, way: Long) = n.edges.indices.single {
        n.edges[it].way == way && n.nodes[n.edges[it].from].id == from && n.nodes[n.edges[it].to].id == to
    }

    /** Directed BFS; deterministic because outgoing lists keep network order. Returns edge path or null. */
    private fun path(allowed: Set<Int>, from: Int, to: Int, within: (Int) -> Boolean = { true }): List<Int>? {
        val parent = HashMap<Int, Int>()
        val seen = hashSetOf(from)
        val queue = ArrayDeque(listOf(from))
        while (queue.isNotEmpty()) {
            val u = queue.removeFirst()
            if (u == to) return generateSequence(parent[to]) { e -> parent[n.edges[e].from] }.toList().reversed()
            for (e in n.outgoing[u].orEmpty()) if (e in allowed && within(e) && seen.add(n.edges[e].to)) {
                parent[n.edges[e].to] = e; queue.addLast(n.edges[e].to)
            }
        }
        return null
    }

    private fun reach(allowed: Set<Int>, start: Int, forward: Boolean): Set<Int> {
        val adjacency = HashMap<Int, MutableList<Int>>()
        for (e in allowed) {
            val (a, b) = n.edges[e].let { if (forward) it.from to it.to else it.to to it.from }
            adjacency.getOrPut(a) { mutableListOf() }.add(b)
        }
        val seen = hashSetOf(start)
        val queue = ArrayDeque(listOf(start))
        while (queue.isNotEmpty()) adjacency[queue.removeFirst()].orEmpty().forEach { if (seen.add(it)) queue.addLast(it) }
        return seen
    }

    /** The policy's main network: its largest strongly connected component (reachable and leavable). */
    private fun main(allowed: Set<Int>): Set<Int> = largestStrongComponent(n, allowed).also {
        assertTrue(it.size > 20000, "main bus/car network must be citywide, got ${it.size}")
    }

    private val route21 = Json.parseToJsonElement(
        checkNotNull(javaClass.getResourceAsStream("/step61b/route21.json")).bufferedReader().readText()
    ).jsonObject

    @Test fun `access record is small, provenance-backed and only corrects bus access`() {
        val ways = n.edges.groupBy { it.way }
        assertEquals(setOf("osm-tag", "verified-service"), access.records.map { it.basis }.toSet())
        for (r in access.records) {
            val edges = assertNotNull(ways[r.way], "record way ${r.way} must exist in the bundled graph")
            assertTrue(edges.all { it.bus != r.bus }, "record ${r.way} must change the importer's bus flag")
            if (r.basis == "verified-service") assertEquals(listOf("21"), r.services)
        }
        // Pinned: widening bus access needs new evidence and review, never a blanket class change.
        assertEquals(setOf(26154806L, 1050773509L, 444474485L, 1212234765L, 391478867L, 1313592331L),
            access.records.filter { it.bus }.map { it.way }.toSet())
        val terminals = access.turnaroundNodes(n)
        assertEquals(access.turnarounds.size, terminals.size, "every turnaround facility is a graph node")
        assertFalse(node(286409016L) in terminals, "Bergsbron's north end is not a turning facility")
        // Policies other than bus are unchanged by the record.
        for (kind in listOf("compact", "estate", "truck")) assertEquals(before.getValue(kind), after.getValue(kind), kind)
        assertTrue(after.getValue("bus").containsAll(before.getValue("bus")))
    }

    @Test fun `Skvallertorget is served by buses in both directions`() {
        val stops = listOf(3183225928L, 2873168580L)
        stops.forEach { assertEquals("Skvallertorget", n.nodes[node(it)].busStop) }
        val now = main(after.getValue("bus"))
        stops.forEach { assertTrue(node(it) in now, "Skvallertorget $it must be reachable and leavable by bus") }
        // Step 6.1 regression reproduced: the southbound stop was lost.
        assertFalse(node(3183225928L) in main(before.getValue("bus")))
    }

    @Test fun `route 21 corridor follows its mapped ways between every mapped stop`() {
        val bus = after.getValue("bus")
        val graphStops = mutableListOf<String>()
        for (relation in route21.getValue("relations").jsonArray.map { it.jsonObject }) {
            val ways = relation.getValue("ways").jsonArray.map { it.jsonPrimitive.long }.toSet()
            val stops = relation.getValue("stops").jsonArray.map { it.jsonObject }
                .filter { it.getValue("node").jsonPrimitive.long in index }
            assertEquals(listOf("Norr Tull", "Bråddgatan", "Skvallertorget", "Väster Tull").let {
                if (relation.getValue("id").jsonPrimitive.long == 14025630L) it.reversed() else it
            }, stops.map { it.getValue("name").jsonPrimitive.content })
            for ((a, b) in stops.zipWithNext()) {
                val from = node(a.getValue("node").jsonPrimitive.long)
                val to = node(b.getValue("node").jsonPrimitive.long)
                val leg = assertNotNull(path(bus, from, to) { n.edges[it].way in ways },
                    "route 21 ${relation["id"]} ${a["name"]} -> ${b["name"]} must follow mapped route ways")
                if (setOf(a, b).any { it.getValue("name").jsonPrimitive.content == "Väster Tull" } &&
                    setOf(a, b).any { it.getValue("name").jsonPrimitive.content == "Skvallertorget" }) {
                    val used = leg.map { n.edges[it].way }.toSet()
                    assertTrue(used.containsAll(setOf(26154805L, 26154806L, 1050773509L, 444474485L, 1212234765L)),
                        "Skvallertorget <-> Väster Tull crosses Bergsbron and the Kungsgatan gångfartsområde")
                    assertNull(path(before.getValue("bus"), from, to) { n.edges[it].way in ways }, "Step 6.1 lost this leg")
                }
                graphStops += "${a["name"]}>${b["name"]}"
            }
        }
        assertEquals(6, graphStops.size)
        // Explicit missing dependency, not an invented route: the Resecentrum and Söder Tull terminal
        // approaches are bus-only/pedestrian-street ways that the importer never put in the graph.
        val ways = n.edges.map { it.way }.toSet()
        for (missing in listOf(286505037L, 297690892L, 1050773511L, 287129554L, 287143362L, 760535027L, 287098415L, 1050773510L))
            assertFalse(missing in ways, "way $missing now exists: extend route 21 coverage to the terminal")
        for (stop in listOf(2756235452L, 1307146456L, 2908154918L)) assertFalse(stop in index)
    }

    @Test fun `Bergsbron keeps trucks out of the trap while buses and cars cross both ways`() {
        val north = edge(286409009L, 286409016L, 26154805L)
        val south = edge(286409016L, 286409009L, 26154805L)
        val truck = after.getValue("truck")
        assertTrue(north in policyEdges(n, "truck", access), "trucks may physically use the bridge")
        assertFalse(north in truck, "a truck must not enter a bridge whose onward street it may not use")
        assertTrue(south in truck)
        for (kind in listOf("bus", "compact")) {
            val allowed = after.getValue(kind)
            assertTrue(north in allowed && south in allowed, "$kind crosses Bergsbron")
            val up = assertNotNull(path(allowed, node(286409009L), node(3183225928L)), "$kind northbound to Skvallertorget")
            val down = assertNotNull(path(allowed, node(2873168580L), node(286409009L)), "$kind southbound from Skvallertorget")
            for (p in listOf(up, down)) for ((a, b) in p.zipWithNext())
                assertFalse(n.edges[a].from == n.edges[b].to && n.edges[a].to == n.edges[b].from, "$kind reverses on $a")
            assertTrue(up.first() == north && down.last() == south)
        }
    }

    @Test fun `every retained edge exits without reversing unless it ends at a legitimate terminus`() {
        val terminals = access.turnaroundNodes(n)
        for ((kind, allowed) in after) for (id in allowed) {
            val e = n.edges[id]
            val realEnd = n.outgoing[e.to].orEmpty().all { n.edges[it].to == e.from }
            assertTrue(realEnd || e.to in terminals || n.outgoing[e.to].orEmpty().any { it in allowed && n.edges[it].to != e.from },
                "$kind edge $id would force an artificial reversal")
        }
        // Existing underpass routes still carry traffic.
        for (way in listOf(39445117L, 89615815L)) assertTrue(after.getValue("compact").any { n.edges[it].way == way })
    }

    @Test fun `mapped turning circles keep their approaches`() {
        val bus = after.getValue("bus")
        val now = main(bus)
        // Platinagatan ends in turning circle 1182562537 with a service road beyond it.
        val platina = n.edges.indices.filter { n.edges[it].way == 4384905L }
        assertTrue(platina.isNotEmpty() && platina.all { it in bus })
        assertFalse(platina.all { it in before.getValue("bus") })
        assertTrue(node(1182562537L) in now)
        // Klockaretorpets centrum (Östgötatrafiken stop positions C/D) sits on a branch of turning circles.
        for (stop in listOf(12874407073L, 12874407074L)) {
            assertEquals("Klockaretorpets centrum", n.nodes[node(stop)].busStop)
            assertTrue(node(stop) in now); assertFalse(node(stop) in main(before.getValue("bus")))
        }
    }

    @Test fun `the seven Step 6_2 bus-end approaches are unreachable without forbidden roads`() {
        val candidates = policyEdges(n, "bus", access).toSet()
        val hub = node(3183225928L) // Skvallertorget, inside the main bus network
        assertTrue(hub in main(after.getValue("bus")))
        val fromHub = reach(candidates, hub, true)
        val toHub = reach(candidates, hub, false)
        for (way in listOf(4498925L, 89884762L, 91258086L, 954899965L, 1050223617L, 1059584521L, 1115174620L)) {
            val edges = n.edges.indices.filter { n.edges[it].way == way }
            assertTrue(edges.isNotEmpty() && edges.all { it in candidates })
            assertTrue(edges.none { n.edges[it].from in fromHub || n.edges[it].to in toHub },
                "way $way is an isolated bus island: its only links are roads closed to buses")
        }
    }

    @Test fun `bus stops outside the main bus network are exactly the documented cases`() {
        val now = main(after.getValue("bus"))
        val outside = n.nodes.indices.filter { n.nodes[it].busStop != null && it !in now }.map { n.nodes[it].id }.toSet()
        // Slåttergatan B/D: Östgötatrafiken stop positions whose onward street (living_street 284799165
        // and service roads) has no verified bus service. The others have no bus-permitted road at all
        // (unverified relation-only service ways or bus-only ways absent from the graph).
        assertEquals(setOf(2920494853L, 13228582396L, 1072428031L, 1531030373L, 1636576788L, 2758041623L,
            2911943540L, 4366727116L, 1041814712L, 1037857208L, 4121120335L, 4121120336L, 4296862604L), outside)
        assertEquals(262, n.nodes.count { it.busStop != null })
    }

    @Test fun `turnaround facility keeps a spur that only a driveway continues`() {
        // 0-1-2 loop; spur 1-3-4 ends at turning circle 4, whose driveway 4-5 is closed to the policy.
        val nodes = (0..5).map { RoadNode(it.toLong(), it.toDouble(), 0.0) }
        val pairs = listOf(0 to 1, 1 to 2, 2 to 0, 1 to 3, 3 to 1, 3 to 4, 4 to 3, 4 to 5, 5 to 4)
        val net = TransportNetwork(nodes, pairs.map { (a, b) -> RoadEdge(a, b, 1.0, .5, .3, true, "T", 1, true) }, emptyList(), emptyList())
        val candidates = net.edges.indices.filter { it != 7 && it != 8 }
        assertEquals(setOf(0, 1, 2, 4, 6), throughRoadEdges(net, candidates), "spur is exit-only without the facility")
        assertEquals(setOf(0, 1, 2, 3, 4, 5, 6), throughRoadEdges(net, candidates, setOf(4)))
        // With no exit at all a turning circle cannot help.
        assertEquals(setOf(0, 1, 2), throughRoadEdges(net, listOf(0, 1, 2, 3), setOf(3)))
    }
}
