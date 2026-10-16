package com.norrkoping.simulator

import kotlin.math.*
import kotlin.random.Random

/** Mutable write aggregate. Only SimulationService may access this under its mutex. */
class SwarmWorld(private val random: Random = Random(42)) {
    private data class Drone(val id: String, var pos: Vec3, var target: Vec3, val home: Vec3, val weight: Double, var battery: Double = 100.0, var state: String = "DELIVERING", var mission: String = "DELIVERING", var recycle: Boolean = true, var idleTime: Double = 0.0, var place: String = "", var route: MutableList<Vec3> = mutableListOf()) {
        fun view() = DroneView(id, pos.array(), target.array(), battery, state, weight, route.map { it.array() }, place)
    }
    val hubs = CityMap.hubs
    private val drones = linkedMapOf<String, Drone>()
    private val zones = linkedMapOf<String, ZoneView>()
    private var nextDrone = 1
    private var nextZone = 1
    var paused = false; private set
    var time = 0.0; private set
    private var wind = Vec3.ZERO
    private fun plan(d: Drone) {
        d.route = Pathfinder.route(d.pos, d.target, zones.values).toMutableList()
        d.state = if (d.route.isEmpty()) "HOLDING" else d.mission
    }
    /** Draws a delivery address from the Norrköping city map; a named request always wins. */
    private fun drawPlace(requested: String?): CityMap.Place =
        CityMap.place(requested) ?: CityMap.places[random.nextInt(CityMap.places.size)]
    private fun destination(altitude: Double, place: CityMap.Place): Vec3 =
        Vec3(place.x + random.nextDouble(-1.2, 1.2), altitude, place.z + random.nextDouble(-1.2, 1.2))
    fun handle(command: Command): List<DomainEvent> {
        val events = mutableListOf<DomainEvent>()
        when (command) {
            is Command.Dispatch -> {
                val r = command.request
                require(r.count in 1..20) { "Count must be between 1 and 20." }
                require(drones.size + r.count <= 100) { "Fleet limit is 100 drones. Reset to clear the fleet." }
                require(r.weightKg.isFinite() && r.weightKg in 0.1..5.0) { "Payload must be between 0.1 and 5 kg." }
                val hub = hubs.find { it.id == r.originHub } ?: throw IllegalArgumentException("Unknown origin hub.")
                require(r.destination == null || CityMap.place(r.destination) != null) { "Unknown destination \"${r.destination}\". Choose one of the places on the city map." }
                repeat(r.count) {
                    val altitude = 18.0 + (nextDrone % 3) * 4
                    val home = Vec3(hub.pos[0], altitude, hub.pos[2])
                    val place = drawPlace(r.destination)
                    val d = Drone("D-${nextDrone++.toString().padStart(3, '0')}", home, destination(altitude, place), home, r.weightKg, place = place.name)
                    plan(d); drones[d.id] = d; events.add(DomainEvent.DroneChanged(d.view()))
                }
                val to = CityMap.place(r.destination)?.let { " to ${it.name}" } ?: ""
                events.add(DomainEvent.Logged("dispatch", "Dispatched ${r.count} drone(s) from ${hub.label}$to."))
            }
            is Command.AddZone -> {
                val r = command.request
                require(zones.size < 24) { "Zone limit is 24." }
                require(r.center.size == 3 && r.center.all { it.isFinite() }) { "Center must contain exactly three finite coordinates." }
                require(abs(r.center[0]) <= 65 && abs(r.center[2]) <= 65 && r.center[1] in 0.0..35.0) { "Zone center is outside the flight area." }
                require(r.radius.isFinite() && r.radius in 4.0..18.0) { "Radius must be between 4 and 18 metres." }
                val zone = ZoneView("Z-$nextZone", r.center.toList(), r.radius)
                val c = Vec3(r.center[0], r.center[1], r.center[2])
                require(hubs.none { hypot(it.pos[0] - c.x, it.pos[2] - c.z) < r.radius + 6 }) { "Keep zones clear of the hubs." }
                require(drones.values.none { !Pathfinder.clear(it.pos, it.pos, listOf(zone)) }) { "A drone is inside this zone. Choose a clear area or try again." }
                zones[zone.id] = zone; nextZone++
                events.add(DomainEvent.ZoneAdded(zone))
                drones.values.filter { it.state != "IDLE" }.forEach { plan(it); events.add(DomainEvent.DroneChanged(it.view())) }
                events.add(DomainEvent.Logged("zone", "${zone.id} activated. Routes recalculated."))
            }
            is Command.RemoveZone -> {
                require(zones.remove(command.id) != null) { "Unknown zone." }
                events.add(DomainEvent.ZoneRemoved(command.id))
                drones.values.filter { it.state != "IDLE" }.forEach { plan(it); events.add(DomainEvent.DroneChanged(it.view())) }
                events.add(DomainEvent.Logged("zone", "${command.id} removed."))
            }
            is Command.Pause -> { paused = command.paused; events.add(DomainEvent.Paused(paused)); events.add(DomainEvent.Logged("system", if (paused) "Simulation paused." else "Simulation resumed.")) }
            is Command.Wind -> {
                val r = command.request
                require(r.x.isFinite() && r.z.isFinite() && abs(r.x) <= 10 && abs(r.z) <= 10) { "Wind components must be between -10 and 10 m/s." }
                wind = Vec3(r.x, 0.0, r.z); events.add(DomainEvent.WindChanged(listOf(r.x, r.z)))
                events.add(DomainEvent.Logged("weather", "Wind set to ${r.x}, ${r.z} m/s."))
            }
            Command.Recall -> {
                drones.values.forEach { it.recycle = false }
                drones.values.filter { it.state != "IDLE" }.forEach { it.recycle = false; it.target = it.home; it.mission = "RETURNING"; plan(it); events.add(DomainEvent.DroneChanged(it.view())) }
                events.add(DomainEvent.Logged("recall", "All active drones recalled to their hubs."))
            }
            Command.Reset -> {
                drones.clear(); zones.clear(); paused = false; time = 0.0; wind = Vec3.ZERO
                events.add(DomainEvent.Reset)
                events.addAll(handle(Command.Dispatch(DispatchRequest(count = 8))))
            }
        }
        return events
    }
    fun tick(dt: Double): List<DomainEvent> {
        require(dt.isFinite() && dt > 0 && dt <= 1)
        if (paused) return emptyList()
        time += dt
        val events = mutableListOf<DomainEvent>()
        for (d in drones.values) {
            if (d.state == "IDLE") {
                d.idleTime += dt
                d.battery = (d.battery + dt * 12).coerceAtMost(100.0)
                if (d.recycle && d.idleTime >= 4 && d.battery >= 99) {
                    val place = drawPlace(null)
                    d.idleTime = 0.0; d.place = place.name; d.target = destination(d.home.y, place); d.mission = "DELIVERING"; plan(d)
                    events.add(DomainEvent.Logged("dispatch", "${d.id} recharged and dispatched again to ${place.name}."))
                }
                events.add(DomainEvent.DroneChanged(d.view()))
                continue
            }
            if (d.state == "EMERGENCY") continue
            if (d.battery < 22 && d.mission == "DELIVERING") {
                d.target = d.home; d.mission = "RETURNING"; plan(d)
                events.add(DomainEvent.Logged("battery", "${d.id} returning on low battery."))
            }
            d.battery = (d.battery - dt * (0.25 + d.weight * 0.04)).coerceAtLeast(0.0)
            if (d.battery == 0.0) {
                d.state = "EMERGENCY"; d.route.clear()
                events.add(DomainEvent.Logged("alert", "${d.id} depleted its battery. Emergency hover marker."))
            } else if (d.route.isNotEmpty()) {
                val delta = d.route.first() - d.pos
                val direction = delta.normalized()
                val speed = (16 - d.weight + wind.dot(direction)).coerceIn(4.0, 25.0)
                val step = min(speed * dt, delta.length())
                val next = d.pos + direction * step
                if (Pathfinder.clear(d.pos, next, zones.values)) {
                    d.pos = next; events.add(DomainEvent.Travelled(step))
                    if (delta.length() <= step + 1e-6) d.route.removeAt(0)
                    if (d.route.isEmpty()) {
                        if (d.mission == "DELIVERING") {
                            events.add(DomainEvent.Delivered)
                            events.add(DomainEvent.Logged("delivery", "${d.id} delivered its payload at ${d.place}."))
                            d.target = d.home; d.mission = "RETURNING"; plan(d)
                        } else { d.state = "IDLE"; d.idleTime = 0.0; events.add(DomainEvent.Logged("arrival", "${d.id} landed at its hub.")) }
                    }
                } else { d.state = "HOLDING"; d.route.clear() }
            }
            events.add(DomainEvent.DroneChanged(d.view()))
        }
        return events
    }
}