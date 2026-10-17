package com.norrkoping.simulator

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlin.math.*

@Serializable data class RoadNode(val id: Long, val x: Double, val z: Double, val y: Double = .071, val signal: Boolean = false, val signalHorizontal: Boolean? = null, val busStop: String? = null)
@Serializable data class RoadEdge(val from: Int, val to: Int, val length: Double, val speed: Double, val width: Double, val truck: Boolean, val name: String, val way: Long, val oneway: Boolean, val bus: Boolean = true)
@Serializable data class Airport(val id: String, val name: String, val runway: String, val start: List<Double>, val end: List<Double>, val width: Double, val historic: Boolean)
@Serializable data class Airway(val id: Long, val kind: String, val points: List<List<Double>>)
@Serializable data class TransitStop(val distance: Double, val name: String)
@Serializable data class TransitRoute(val id: String, val kind: String, val name: String, val loop: Boolean, val points: List<List<Double>>, val stops: List<TransitStop>, val destinations: List<TransitStop> = emptyList())
@Serializable data class TransportNetwork(val nodes: List<RoadNode>, val edges: List<RoadEdge>, val airports: List<Airport>, val airways: List<Airway>, val transitRoutes: List<TransitRoute> = emptyList()) {
    val outgoing by lazy { edges.indices.groupBy { edges[it].from } }
    val junctions by lazy { edges.groupBy { it.from }.filterValues { es -> es.map { it.to }.distinct().size > 2 }.keys }
    companion object {
        private val codec = Json { ignoreUnknownKeys = true }
        val bundled by lazy { codec.decodeFromString<TransportNetwork>(checkNotNull(TransportNetwork::class.java.getResourceAsStream("/transport-network.json")).bufferedReader().use { it.readText() }) }
    }
}
@Serializable data class TrafficSettings(val compact: Int = 100, val estate: Int = 60, val trucks: Int = 20, val flights: Boolean = true, val timeScale: Double = 3.0, val buses: Int = 0, val trams: Int = 0, val trains: Int = 0)
@Serializable data class VehicleSection(val pos: List<Double>, val heading: Double)
@Serializable data class VehicleView(val id: Int, val kind: String, val pos: List<Double>, val heading: Double, val speed: Double, val road: String, val stopped: Boolean, val sections: List<VehicleSection> = emptyList(), val stop: String? = null, val destination: String? = null)
@Serializable data class AircraftView(val id: String = "NK-01", val pos: List<Double>, val heading: Double, val pitch: Double, val bank: Double, val phase: String, val origin: String, val destination: String, val speed: Double, val gear: Boolean)
@Serializable data class CitySnapshot(val sequence: Long, val simulationTime: Double, val paused: Boolean, val settings: TrafficSettings, val vehicles: List<VehicleView>, val aircraft: List<AircraftView>, val completedFlights: Int, val events: List<Activity>, val terrainRevision: String = TerrainService.bundled.data.revision)
sealed interface CityCommand {
    data class Configure(val settings: TrafficSettings): CityCommand
    data class Pause(val value: Boolean): CityCommand
    data object Reset: CityCommand
}

/** Cars and buses share one occupancy model; rails have their own continuous paths. */
class RoadTraffic(private val network: TransportNetwork = TransportNetwork.bundled, private val terrain: TerrainService = TerrainService.bundled) {
    private val random = kotlin.random.Random(1309)
    private data class Car(val id: Int, val kind: String, var edge: Int, var progress: Double, var speed: Double = 0.0, var previous: Int = edge, val path: ArrayDeque<Int> = ArrayDeque(), var dwellUntil: Double = 0.0, var lastStop: Int = -1)
    private data class Occupant(val id: Int, val progress: Double, val length: Double)
    private data class Reservation(val owner: Int, var until: Double)
    private val cars = mutableListOf<Car>()
    private var nextId = 1
    private val reservations = mutableMapOf<Int, Reservation>()
    private fun length(car: Car) = when(car.kind) { "truck" -> .49; "bus" -> .48; "estate" -> .19; else -> .16 }
    // Eliminate directed traps, including clipped motorway ends, before choosing a route.
    private val allowed = listOf("compact", "estate", "truck", "bus").associateWith { kind ->
        val candidates = network.edges.indices.filter { kind != "truck" || network.edges[it].truck }.filter { kind != "bus" || network.edges[it].bus }
        val counts = IntArray(network.nodes.size)
        val incoming = candidates.groupBy { network.edges[it].to }
        candidates.forEach { counts[network.edges[it].from]++ }
        val removed = mutableSetOf<Int>()
        val queue = ArrayDeque(network.nodes.indices.filter { counts[it] == 0 })
        while(queue.isNotEmpty()) for(edge in incoming[queue.removeFirst()].orEmpty()) {
            if(!removed.add(edge)) continue
            val from = network.edges[edge].from
            if(--counts[from] == 0) queue.add(from)
        }
        candidates.filter { it !in removed }.ifEmpty { candidates }.toSet()
    }
    private val spawnEdges = allowed.mapValues { (_, set) -> set.filter { network.edges[it].length > .7 && network.outgoing[network.edges[it].to]?.isNotEmpty() == true } }
    fun configure(settings: TrafficSettings) {
        for((kind, count) in listOf("compact" to settings.compact, "estate" to settings.estate, "truck" to settings.trucks, "bus" to settings.buses)) {
            val current = cars.filter { it.kind == kind }
            if(current.size > count) cars.removeAll(current.drop(count).toSet())
            if(count <= current.size) continue
            val pool = spawnEdges.getValue(kind)
            require(pool.isNotEmpty()) { "No drivable roads for $kind" }
            repeat(count-current.size) {
                var edge = pool.random(random); var progress = 0.0
                for(attempt in 0..2000) {
                    edge = pool.random(random); progress = random.nextDouble()*network.edges[edge].length
                    if(cars.none { it.edge == edge && abs(it.progress-progress) < .8 }) break
                }
                cars.add(Car(nextId++,kind,edge,progress))
            }
        }
        val live=cars.map { it.id }.toSet()
        reservations.entries.removeIf { it.value.owner !in live }
    }
    private fun direction(edge: Int): Pair<Double,Double> {
        val e=network.edges[edge];val a=network.nodes[e.from];val b=network.nodes[e.to]
        return (b.x-a.x)/e.length to (b.z-a.z)/e.length
    }
    private fun fillPath(car: Car) {
        var edge=car.path.lastOrNull() ?: car.edge
        while(car.path.size<32) {
            val old=network.edges[edge];val d=direction(edge)
            val choices=network.outgoing[old.to].orEmpty().filter { it in allowed.getValue(car.kind) }
            val onward=choices.filter { network.edges[it].to != old.from }.ifEmpty { choices }
            if(onward.isEmpty()) break
            val weights=onward.map { val n=direction(it);max(.08,1.0+d.first*n.first+d.second*n.second) }
            var sample=random.nextDouble()*weights.sum();var chosen=onward.last()
            for(i in onward.indices) { sample-=weights[i];if(sample<=0) { chosen=onward[i];break } }
            car.path.addLast(chosen);edge=chosen
        }
    }
    private fun green(node: RoadNode, edge: Int, time: Double): Boolean {
        val d=direction(edge);val horizontal=node.signalHorizontal ?: (abs(d.first)>abs(d.second))
        val phase=time%48
        return if(horizontal) phase<20 else phase>=24 && phase<44
    }
    fun tick(dt: Double, time: Double) {
        reservations.entries.removeIf { it.value.until < time }
        cars.forEach(::fillPath)
        val occupancy=cars.groupBy { it.edge }.mapValues { (_, cs) -> cs.map { Occupant(it.id,it.progress,length(it)) } }
        for(car in cars) {
            val e=network.edges[car.edge]
            if(car.lastStop != e.from && car.lastStop != e.to) car.lastStop = -1
            var gap=Double.POSITIVE_INFINITY
            occupancy[car.edge].orEmpty().filter { it.id!=car.id && it.progress>car.progress }.forEach { gap=min(gap,it.progress-car.progress-(it.length+length(car))/2) }
            var remaining=e.length-car.progress
            val visited=mutableSetOf(car.edge)
            for(next in car.path) {
                // Never reinterpret cars behind us as obstacles ahead after a short route loop.
                if(remaining>4 || !visited.add(next)) break
                occupancy[next].orEmpty().filter { it.id!=car.id }.forEach { gap=min(gap,remaining+it.progress-(it.length+length(car))/2) }
                remaining+=network.edges[next].length
            }
            var signalDistance=e.length-car.progress
            val signalVisited=mutableSetOf<Int>()
            for(edge in listOf(car.edge)+car.path) {
                if(signalDistance>4 || !signalVisited.add(edge)) break
                val n=network.nodes[network.edges[edge].to]
                if(n.signal && !green(n,edge,time)) { gap=min(gap,signalDistance-length(car)/2-.04);break }
                val nextIndex=if(edge==car.edge) car.path.firstOrNull() else car.path.toList().let { val i=it.indexOf(edge);it.getOrNull(i+1) }
                if(nextIndex!=null) signalDistance+=network.edges[nextIndex].length
            }
            val left=e.length-car.progress
            val n=network.nodes[e.to]
            if(e.to in network.junctions && left<max(.9,car.speed*2)) {
                val owner=reservations[e.to]
                val permitted=!n.signal || green(n,car.edge,time)
                if(owner==null && permitted && gap>left+length(car)/2+.12) reservations[e.to]=Reservation(car.id,time+8)
                if(reservations[e.to]?.owner!=car.id) gap=min(gap,left-length(car)/2-.04)
            }
            if(car.kind=="bus" && n.busStop!=null && left<.32 && car.lastStop!=e.to) {
                car.lastStop=e.to;car.dwellUntil=time+8
            }
            val limit=min(e.speed,when(car.kind) { "truck" -> .89; "bus" -> .61; else -> 1.23 })
            val desired=if(time<car.dwellUntil) 0.0 else min(limit,max(0.0,(gap-.10)/1.2))
            car.speed+=(desired-car.speed).coerceIn(-.25*dt,.075*dt)
            var distance=min(car.speed*dt,max(0.0,gap-.06))
            while(distance>0) {
                val edge=network.edges[car.edge];val rest=edge.length-car.progress
                if(distance<rest) { car.progress+=distance;break }
                if(car.path.isEmpty()) { car.progress=edge.length;car.speed=0.0;break }
                reservations[edge.to]?.takeIf { it.owner==car.id }?.until=time+(length(car)+.1)/max(.15,car.speed)
                distance-=rest;car.previous=car.edge;car.edge=car.path.removeFirst();car.progress=0.0
            }
        }
    }
    fun views(): List<VehicleView> = cars.map { car ->
        val e=network.edges[car.edge];val a=network.nodes[e.from];val b=network.nodes[e.to];val t=(car.progress/e.length).coerceIn(0.0,1.0)
        val d=direction(car.edge);val previous=direction(car.previous);val next=direction(car.path.firstOrNull()?:car.edge)
        val blend=min(.24,e.length*.3)
        val start=(1-car.progress/blend).coerceIn(0.0,1.0)*.5
        val end=(1-(e.length-car.progress)/blend).coerceIn(0.0,1.0)*.5
        var dx=d.first*(1-start-end)+previous.first*start+next.first*end
        var dz=d.second*(1-start-end)+previous.second*start+next.second*end
        val norm=max(hypot(dx,dz),.001);dx/=norm;dz/=norm
        val offset=if(e.oneway && e.width<.24) 0.0 else min(e.width*.25,.075)
        VehicleView(car.id,car.kind,listOf(a.x+(b.x-a.x)*t-dz*offset,terrain.profileHeight(listOf(a.x,a.y,a.z),listOf(b.x,b.y,b.z),t)+.008,a.z+(b.z-a.z)*t+dx*offset),atan2(dx,dz),car.speed*90,e.name,car.speed<.015,stop=if(car.kind=="bus"&&car.speed<.015)network.nodes.getOrNull(car.lastStop)?.busStop else null)
    }
}
