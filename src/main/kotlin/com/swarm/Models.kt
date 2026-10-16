package com.swarm

import kotlinx.serialization.Serializable
import kotlin.math.sqrt

@Serializable
data class Vec3(val x: Double, val y: Double, val z: Double) {
    operator fun plus(v: Vec3) = Vec3(x + v.x, y + v.y, z + v.z)
    operator fun minus(v: Vec3) = Vec3(x - v.x, y - v.y, z - v.z)
    operator fun times(n: Double) = Vec3(x * n, y * n, z * n)
    fun dot(v: Vec3) = x * v.x + y * v.y + z * v.z
    fun length() = sqrt(dot(this))
    fun normalized() = if (length() < 1e-8) ZERO else this * (1 / length())
    fun array() = listOf(x, y, z)
    companion object { val ZERO = Vec3(0.0, 0.0, 0.0) }
}
@Serializable data class DispatchRequest(val count: Int = 1, val originHub: String = "HUB_HAMNEN", val weightKg: Double = 2.0, val destination: String? = null)
@Serializable data class ZoneRequest(val center: List<Double>, val radius: Double = 12.0)
@Serializable data class PauseRequest(val paused: Boolean)
@Serializable data class WindRequest(val x: Double = 0.0, val z: Double = 0.0)
@Serializable data class IdRequest(val id: String)
@Serializable data class CommandResult(val message: String)
@Serializable data class ApiError(val error: String)
@Serializable data class DroneView(val id: String, val pos: List<Double>, val target: List<Double>, val battery: Double, val state: String, val weightKg: Double, val route: List<List<Double>>, val destination: String = "")
@Serializable data class ZoneView(val id: String, val center: List<Double>, val radius: Double)
@Serializable data class HubView(val id: String, val label: String, val pos: List<Double>)
@Serializable data class Activity(val id: Long, val time: Double, val kind: String, val message: String)
@Serializable data class WorldSnapshot(val sequence: Long = 0, val simulationTime: Double = 0.0, val paused: Boolean = false, val wind: List<Double> = listOf(0.0, 0.0), val drones: List<DroneView> = emptyList(), val noFlyZones: List<ZoneView> = emptyList(), val hubs: List<HubView> = emptyList())
@Serializable data class TelemetrySnapshot(val sequence: Long = 0, val active: Int = 0, val delivering: Int = 0, val returning: Int = 0, val holding: Int = 0, val deliveries: Int = 0, val averageBattery: Double = 0.0, val distanceMetres: Double = 0.0, val events: List<Activity> = emptyList())

sealed interface Command {
    data class Dispatch(val request: DispatchRequest) : Command
    data class AddZone(val request: ZoneRequest) : Command
    data class RemoveZone(val id: String) : Command
    data class Pause(val paused: Boolean) : Command
    data class Wind(val request: WindRequest) : Command
    data object Recall : Command
    data object Reset : Command
}
sealed interface DomainEvent {
    data class DroneChanged(val drone: DroneView) : DomainEvent
    data class ZoneAdded(val zone: ZoneView) : DomainEvent
    data class ZoneRemoved(val id: String) : DomainEvent
    data class Paused(val value: Boolean) : DomainEvent
    data class WindChanged(val value: List<Double>) : DomainEvent
    data class Travelled(val metres: Double) : DomainEvent
    data object Delivered : DomainEvent
    data object Reset : DomainEvent
    data class Logged(val kind: String, val message: String) : DomainEvent
}