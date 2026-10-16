package com.swarm

import kotlinx.coroutines.*
import kotlinx.coroutines.flow.first
import kotlin.test.*

class SimulationTest {
    @Test fun `path routes around a sphere with clearance on every segment`() {
        val start = Vec3(-40.0, 18.0, 0.0)
        val end = Vec3(40.0, 18.0, 0.0)
        val zones = listOf(ZoneView("Z-1", listOf(0.0, 15.0, 0.0), 12.0))
        assertFalse(Pathfinder.clear(start, end, zones))
        val route = Pathfinder.route(start, end, zones)
        assertTrue(route.size > 1)
        assertEquals(end, route.last())
        (listOf(start) + route).zipWithNext().forEach { (a, b) -> assertTrue(Pathfinder.clear(a, b, zones)) }
    }
    @Test fun `blocked destination has no route`() {
        assertTrue(Pathfinder.route(Vec3(-40.0, 18.0, 0.0), Vec3(0.0, 18.0, 0.0), listOf(ZoneView("z", listOf(0.0, 15.0, 0.0), 12.0))).isEmpty())
    }
    @Test fun `fleet serves the Norrköping city map`() = runBlocking {
        val service = SimulationService()
        assertEquals(listOf("HUB_HAMNEN", "HUB_VRINNEVI"), service.world.value.hubs.map { it.id })
        assertEquals(listOf("Norrköpings hamn", "Vrinnevisjukhuset"), service.world.value.hubs.map { it.label })
        service.execute(Command.Dispatch(DispatchRequest(count = 1, destination = "arbetets museum")))
        val drone = service.world.value.drones.last()
        assertEquals("Arbetets museum", drone.destination)
        val museum = CityMap.places.first { it.name == "Arbetets museum" }
        assertEquals(museum.x, drone.target[0], 1.3)
        assertEquals(museum.z, drone.target[2], 1.3)
        assertTrue(service.world.value.drones.take(8).all { it.destination.isNotBlank() })
        assertFailsWith<IllegalArgumentException> { service.execute(Command.Dispatch(DispatchRequest(destination = "Stockholm"))) }
    }
    @Test fun `validation failures leave projection unchanged`() = runBlocking {
        val service = SimulationService()
        val before = service.world.value
        assertFailsWith<IllegalArgumentException> { service.execute(Command.Dispatch(DispatchRequest(count = 0))) }
        assertFailsWith<IllegalArgumentException> { service.execute(Command.Dispatch(DispatchRequest(originHub = "bad"))) }
        assertFailsWith<IllegalArgumentException> { service.execute(Command.AddZone(ZoneRequest(listOf(1.0, 2.0)))) }
        assertFailsWith<IllegalArgumentException> { service.execute(Command.AddZone(ZoneRequest(listOf(0.0, 15.0, 0.0), -1.0))) }
        assertEquals(before, service.world.value)
    }
    @Test fun `concurrent commands are serialized without lost drones`() = runBlocking {
        val service = SimulationService()
        coroutineScope { repeat(40) { launch(Dispatchers.Default) { service.execute(Command.Dispatch(DispatchRequest())) } } }
        assertEquals(48, service.world.value.drones.size)
        assertEquals(48, service.world.value.drones.map { it.id }.toSet().size)
    }
    @Test fun `pause freezes movement and reset clears telemetry zones and wind`() = runBlocking {
        val service = SimulationService()
        service.execute(Command.Pause(true))
        val before = service.world.value.drones
        repeat(20) { service.step(0.05, it.toLong()) }
        assertEquals(before, service.world.value.drones)
        service.execute(Command.AddZone(ZoneRequest(listOf(0.0, 15.0, 0.0))))
        service.execute(Command.Wind(WindRequest(8.0, 4.0)))
        service.execute(Command.Reset)
        assertEquals(8, service.world.value.drones.size)
        assertTrue(service.world.value.noFlyZones.isEmpty())
        assertFalse(service.world.value.paused)
        assertEquals(listOf(0.0, 0.0), service.world.value.wind)
        assertEquals(0, service.telemetry.value.deliveries)
    }
    @Test fun `late subscribers receive latest snapshot and collectors never block the simulation`() = runBlocking {
        val service = SimulationService()
        val snapshot = service.world.value
        service.execute(Command.Dispatch(DispatchRequest(count = 2)))
        assertEquals(8, snapshot.drones.size)
        assertEquals(10, service.world.first().drones.size)
        withTimeout(1000) { repeat(100) { service.step(0.05, it.toLong()) } }
        assertTrue(service.world.value.simulationTime > 4.9)
    }
    @Test fun `missions deliver and recalled fleet stays home`() = runBlocking {
        val service = SimulationService()
        repeat(600) { service.step(0.05, it.toLong()) }
        assertTrue(service.telemetry.value.deliveries > 0)
        service.execute(Command.Recall)
        repeat(1200) { service.step(0.05, it.toLong()) }
        assertTrue(service.world.value.drones.all { it.state == "IDLE" })
    }
    @Test fun `zone rerouting never allows a drone inside exclusion clearance`() = runBlocking {
        val service = SimulationService()
        service.execute(Command.AddZone(ZoneRequest(listOf(0.0, 15.0, 0.0))))
        repeat(500) { frame ->
            service.step(0.05, frame.toLong())
            val state = service.world.value
            state.drones.forEach { d ->
                val p = Vec3(d.pos[0], d.pos[1], d.pos[2])
                assertTrue(Pathfinder.clear(p, p, state.noFlyZones), "${d.id} entered a zone")
            }
        }
    }
}