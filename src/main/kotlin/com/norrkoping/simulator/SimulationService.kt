package com.norrkoping.simulator

import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** Event-driven read projection; it owns no references to mutable write-side objects. */
class Projections(private val hubs: List<HubView>) {
    private val drones = linkedMapOf<String, DroneView>()
    private val zones = linkedMapOf<String, ZoneView>()
    private val activity = ArrayDeque<Activity>()
    private var deliveries = 0
    private var distance = 0.0
    private var paused = false
    private var wind = listOf(0.0, 0.0)
    private var eventId = 0L
    fun apply(events: List<DomainEvent>, time: Double) {
        events.forEach { event -> when (event) {
            is DomainEvent.DroneChanged -> drones[event.drone.id] = event.drone
            is DomainEvent.ZoneAdded -> zones[event.zone.id] = event.zone
            is DomainEvent.ZoneRemoved -> zones.remove(event.id)
            is DomainEvent.Paused -> paused = event.value
            is DomainEvent.WindChanged -> wind = event.value
            is DomainEvent.Travelled -> distance += event.metres
            DomainEvent.Delivered -> deliveries++
            DomainEvent.Reset -> { drones.clear(); zones.clear(); activity.clear(); deliveries = 0; distance = 0.0; paused = false; wind = listOf(0.0, 0.0) }
            is DomainEvent.Logged -> { activity.addFirst(Activity(++eventId, time, event.kind, event.message)); while (activity.size > 50) activity.removeLast() }
        } }
    }
    fun world(sequence: Long, time: Double) = WorldSnapshot(sequence, time, paused, wind, drones.values.toList(), zones.values.toList(), hubs)
    fun telemetry(sequence: Long) = TelemetrySnapshot(sequence, drones.values.count { it.state != "IDLE" }, drones.values.count { it.state == "DELIVERING" }, drones.values.count { it.state == "RETURNING" }, drones.values.count { it.state == "HOLDING" || it.state == "EMERGENCY" }, deliveries, if (drones.isEmpty()) 0.0 else drones.values.map { it.battery }.average(), distance, activity.toList())
}

class SimulationService {
    private val mutex = Mutex()
    private val aggregate = NorrköpingWorld()
    private val projection = Projections(aggregate.hubs)
    private var sequence = 0L
    private val worldState = MutableStateFlow(WorldSnapshot())
    private val telemetryState = MutableStateFlow(TelemetrySnapshot())
    val world = worldState.asStateFlow()
    val telemetry = telemetryState.asStateFlow()
    init { projection.apply(aggregate.handle(Command.Dispatch(DispatchRequest(count = 8))), 0.0); publish(true) }
    private fun publish(includeTelemetry: Boolean) {
        sequence++
        worldState.value = projection.world(sequence, aggregate.time)
        if (includeTelemetry) telemetryState.value = projection.telemetry(sequence)
    }
    suspend fun execute(command: Command) = mutex.withLock {
        projection.apply(aggregate.handle(command), aggregate.time)
        publish(true)
    }
    suspend fun step(dt: Double, frame: Long) = mutex.withLock {
        projection.apply(aggregate.tick(dt), aggregate.time)
        if (frame % 2L == 0L) publish(frame % 10L == 0L)
    }
    suspend fun run() {
        var frame = 0L
        while (currentCoroutineContext().isActive) {
            val started = System.nanoTime()
            step(0.05, ++frame)
            delay((50 - (System.nanoTime() - started) / 1_000_000).coerceAtLeast(1))
        }
    }
}