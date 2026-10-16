package com.norrkoping.simulator

import kotlin.test.*
import kotlin.math.*
import kotlinx.coroutines.test.runTest

class CityTransportTest {
    private val network get() = TransportNetwork.bundled
    @Test fun `regional network includes airports and excludes Drottninggatan`() {
        assertEquals(setOf("ESSP", "F13"), network.airports.map { it.id }.toSet())
        assertTrue(network.nodes.size > 30000)
        assertTrue(network.nodes.count { it.signal } > 50)
        assertFalse(network.edges.any { it.name.equals("Drottninggatan", true) })
        assertTrue(network.edges.any { it.truck && it.name == "Söderleden" })
        assertTrue(network.edges.all { it.length > 0 && it.length.isFinite() && it.from in network.nodes.indices && it.to in network.nodes.indices })
    }
    @Test fun `vehicle counts routes and truck restrictions survive movement`() {
        val traffic = RoadTraffic()
        traffic.configure(TrafficSettings(32, 16, 12, false, 1.0))
        val initial = traffic.views()
        val truckRoads = network.edges.filter { it.truck }.map { it.name }.toSet()
        repeat(1200) { tick ->
            traffic.tick(.05, tick * .05)
            for (v in traffic.views()) {
                assertTrue(v.pos.all { it.isFinite() })
                assertTrue(v.speed in 0.0..125.0)
                assertNotEquals("Drottninggatan", v.road)
                if (v.kind == "truck") assertTrue(v.road in truckRoads)
            }
        }
        assertEquals(mapOf("compact" to 32, "estate" to 16, "truck" to 12), traffic.views().groupingBy { it.kind }.eachCount())
        assertTrue(traffic.views().zip(initial).count { (a,b) -> a.pos != b.pos } > 40)
        traffic.configure(TrafficSettings(0, 0, 0, false, 1.0))
        assertTrue(traffic.views().isEmpty())
    }
    @Test fun `a red signal stops a vehicle until its direction gets green`() {
        val small = TransportNetwork(
            listOf(RoadNode(1,0.0,0.0),RoadNode(2,5.0,0.0,signal=true),RoadNode(3,10.0,0.0)),
            listOf(RoadEdge(0,1,5.0,.5,.3,true,"Approach",1,true),RoadEdge(1,2,5.0,.5,.3,true,"Exit",1,true)),
            emptyList(),emptyList())
        val traffic=RoadTraffic(small)
        traffic.configure(TrafficSettings(1,0,0,false,1.0))
        repeat(500) { traffic.tick(.05,30.0) }
        val stopped=traffic.views().single()
        assertEquals("Approach",stopped.road)
        assertTrue(stopped.stopped)
        assertTrue(stopped.pos[0]<5.0)
        repeat(400) { traffic.tick(.05,5.0) }
        assertEquals("Exit",traffic.views().single().road)
    }
    @Test fun `flight taxis takes off and lands at both airports without teleportation`() {
        val flight=RegionalFlight()
        val phases=mutableSetOf<String>()
        val arrivals=mutableSetOf<String>()
        var previous=flight.view().pos
        repeat(52000) {
            if(flight.tick(.05)) arrivals.add(flight.view().destination)
            val v=flight.view();phases.add(v.phase)
            assertTrue(v.pos.all { it.isFinite() })
            assertTrue(v.pos[1]>=.099)
            val distance=sqrt(v.pos.zip(previous).sumOf { (a,b)->(a-b).pow(2) })
            assertTrue(distance<.4,"Jump of $distance in ${v.phase}")
            assertTrue(v.speed<720,"Speed ${v.speed} in ${v.phase}")
            previous=v.pos
        }
        assertEquals(setOf("PARKED","TAXI_OUT","TAKEOFF","CLIMB","CRUISE","APPROACH","LANDING","TAXI_IN"),phases)
        assertEquals(setOf("Norrköpings flygplats","Bråvalla F13"),arrivals)
        assertTrue(flight.completed>=2)
    }
    @Test fun `pause freezes traffic and invalid settings do not mutate state`() = runTest {
        val service=CitySimulationService()
        service.execute(CityCommand.Configure(TrafficSettings(4,3,2,false,1.0)))
        service.step(1.0)
        service.execute(CityCommand.Pause(true))
        val before=service.world.value
        service.step(1.0)
        assertEquals(before.vehicles,service.world.value.vehicles)
        assertEquals(before.simulationTime,service.world.value.simulationTime)
        assertEquals(9,before.vehicles.size)
        val sequence=service.world.value.sequence
        assertFailsWith<IllegalArgumentException> { service.execute(CityCommand.Configure(TrafficSettings(600,600,150))) }
        assertEquals(sequence,service.world.value.sequence)
    }
}
