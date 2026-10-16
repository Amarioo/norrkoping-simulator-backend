package com.swarm

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlin.math.*

@Serializable data class RoadNode(val id: Long, val x: Double, val z: Double, val y: Double = .071, val signal: Boolean = false)
@Serializable data class RoadEdge(val from: Int, val to: Int, val length: Double, val speed: Double, val width: Double, val truck: Boolean, val name: String, val way: Long, val oneway: Boolean)
@Serializable data class Airport(val id: String, val name: String, val runway: String, val start: List<Double>, val end: List<Double>, val width: Double, val historic: Boolean)
@Serializable data class Airway(val id: Long, val kind: String, val points: List<List<Double>>)
@Serializable data class TransportNetwork(val nodes: List<RoadNode>, val edges: List<RoadEdge>, val airports: List<Airport>, val airways: List<Airway>) {
    val outgoing: Map<Int,List<Int>> by lazy { edges.indices.groupBy { edges[it].from } }
    val junctions: Set<Int> by lazy { edges.groupBy { it.from }.filterValues { es -> es.map { it.to }.distinct().size > 2 }.keys }
    companion object {
        val bundled by lazy { Json { ignoreUnknownKeys = true }.decodeFromString<TransportNetwork>(checkNotNull(TransportNetwork::class.java.getResourceAsStream("/transport-network.json")).bufferedReader().use { it.readText() }) }
    }
}
@Serializable data class TrafficSettings(val compact: Int = 100, val estate: Int = 60, val trucks: Int = 20, val flights: Boolean = true, val timeScale: Double = 3.0)
@Serializable data class VehicleView(val id: Int, val kind: String, val pos: List<Double>, val heading: Double, val speed: Double, val road: String, val stopped: Boolean)
@Serializable data class AircraftView(val id: String = "NK-01", val pos: List<Double>, val heading: Double, val pitch: Double, val bank: Double, val phase: String, val origin: String, val destination: String, val speed: Double, val gear: Boolean)
@Serializable data class CitySnapshot(val sequence: Long, val simulationTime: Double, val paused: Boolean, val settings: TrafficSettings, val vehicles: List<VehicleView>, val aircraft: List<AircraftView>, val completedFlights: Int, val events: List<Activity>)
sealed interface CityCommand {
    data class Configure(val settings: TrafficSettings): CityCommand
    data class Pause(val value: Boolean): CityCommand
    data object Reset: CityCommand
}

/** Directed OSM network: movement never invents a shortcut across a city block. */
class RoadTraffic(private val network: TransportNetwork = TransportNetwork.bundled) {
    private val random = kotlin.random.Random(1309)
    private data class Car(val id: Int, val kind: String, var edge: Int, var progress: Double, var speed: Double = 0.0, var previous: Int = edge, val path: ArrayDeque<Int> = ArrayDeque())
    private val cars = mutableListOf<Car>()
    private var nextId = 1
    private val reservations = mutableMapOf<Int,Pair<Int,Double>>()
    private val spawnEdges = network.edges.indices.filter { network.edges[it].length > .7 && network.outgoing[network.edges[it].to]?.isNotEmpty() == true }
    private val truckEdges = spawnEdges.filter { network.edges[it].truck }
    private fun length(car: Car) = if (car.kind == "truck") .49 else if (car.kind == "estate") .19 else .16
    fun configure(settings: TrafficSettings) {
        for ((kind,count) in listOf("compact" to settings.compact,"estate" to settings.estate,"truck" to settings.trucks)) {
            val current = cars.filter { it.kind == kind }
            if (current.size > count) cars.removeAll(current.drop(count).toSet())
            val pool = if (kind == "truck") truckEdges else spawnEdges
            require(pool.isNotEmpty()) { "No drivable roads for $kind" }
            repeat((count-current.size).coerceAtLeast(0)) {
                var edge = pool[random.nextInt(pool.size)]; var progress = 0.0
                for (attempt in 0..2000) {
                    edge = pool[random.nextInt(pool.size)]; progress = random.nextDouble()*network.edges[edge].length
                    if(cars.none { it.edge==edge && abs(it.progress-progress)<.8 }) break
                }
                cars.add(Car(nextId++,kind,edge,progress))
            }
        }
    }
    private fun direction(edge: Int): Pair<Double,Double> {val e=network.edges[edge];val a=network.nodes[e.from];val b=network.nodes[e.to];return (b.x-a.x)/e.length to (b.z-a.z)/e.length}
    private fun fillPath(car: Car) {
        var edge = car.path.lastOrNull() ?: car.edge
        while(car.path.size<18) {
            val old=network.edges[edge];val d=direction(edge)
            val choices=network.outgoing[old.to].orEmpty().filter { car.kind!="truck"||network.edges[it].truck }
            val onward=choices.filter { network.edges[it].to!=old.from }.ifEmpty { choices }
            if(onward.isEmpty())break
            // Preserve the lane's direction; turns are chosen at real shared OSM nodes.
            val weights=onward.map { val n=direction(it); max(.08,1.0+d.first*n.first+d.second*n.second) }
            var sample=random.nextDouble()*weights.sum();var chosen=onward.last()
            for(i in onward.indices){sample-=weights[i];if(sample<=0){chosen=onward[i];break}}
            car.path.addLast(chosen);edge=chosen
        }
    }
    fun tick(dt: Double,time: Double) {
        reservations.entries.removeIf { it.value.second<time }
        cars.forEach(::fillPath)
        val occupancy=cars.map { it.copy(path=ArrayDeque()) }.groupBy { it.edge }
        for(car in cars) {
            val e=network.edges[car.edge];var remaining=e.length-car.progress
            var gap=Double.POSITIVE_INFINITY
            occupancy[car.edge].orEmpty().filter { it.id!=car.id&&it.progress>car.progress }.forEach { gap=min(gap,it.progress-car.progress-(length(it)+length(car))/2) }
            for(next in car.path) {
                if(remaining>4)break
                occupancy[next].orEmpty().filter { it.id!=car.id }.forEach { gap=min(gap,remaining+it.progress-(length(it)+length(car))/2) }
                remaining+=network.edges[next].length
            }
            val node=network.nodes[e.to];val d=direction(car.edge)
            val horizontal=abs(d.first)>abs(d.second)
            val phase=time%48;val green=if(horizontal)phase<20 else phase>=24&&phase<44
            if(node.signal&&!green)gap=min(gap,e.length-car.progress-.12)
            if(e.to in network.junctions && e.length-car.progress<max(.5,car.speed*2)) {
                val owner=reservations[e.to]
                if(owner!=null&&owner.first!=car.id)gap=min(gap,e.length-car.progress-.12)
                else if(gap>.15)reservations[e.to]=car.id to (time+max(1.2,(length(car)+.3)/max(.2,car.speed)))
            }
            val limit=min(e.speed,if(car.kind=="truck").89 else 1.23)
            val desired=min(limit,max(0.0,(gap-.12)/1.5))
            car.speed+=(desired-car.speed).coerceIn(-.22*dt,.075*dt)
            var distance=min(car.speed*dt,max(0.0,gap-.07))
            while(distance>0) {
                val left=network.edges[car.edge].length-car.progress
                if(distance<left){car.progress+=distance;break}
                if(car.path.isEmpty()){car.progress=network.edges[car.edge].length;car.speed=0.0;break}
                distance-=left;car.previous=car.edge;car.edge=car.path.removeFirst();car.progress=0.0
            }
        }
    }
    fun views(): List<VehicleView> = cars.map { car ->
        val e=network.edges[car.edge];val a=network.nodes[e.from];val b=network.nodes[e.to];val t=(car.progress/e.length).coerceIn(0.0,1.0)
        val d=direction(car.edge);val previous=direction(car.previous);val next=direction(car.path.firstOrNull()?:car.edge)
        val blend=min(.24,e.length*.3)
        val weightStart=(1-car.progress/blend).coerceIn(0.0,1.0)*.5
        val weightEnd=(1-(e.length-car.progress)/blend).coerceIn(0.0,1.0)*.5
        var dx=d.first*(1-weightStart-weightEnd)+previous.first*weightStart+next.first*weightEnd
        var dz=d.second*(1-weightStart-weightEnd)+previous.second*weightStart+next.second*weightEnd
        val norm=hypot(dx,dz);dx/=max(norm,.001);dz/=max(norm,.001)
        val offset=if(e.oneway && e.width<.24)0.0 else min(e.width*.25,.075)
        // In X-east/Z-south coordinates, (-dz,+dx) is the right-hand traffic lane.
        VehicleView(car.id,car.kind,listOf(a.x+(b.x-a.x)*t-dz*offset,a.y+(b.y-a.y)*t+.012,a.z+(b.z-a.z)*t+dx*offset),atan2(dx,dz),car.speed*25*3.6,e.name,car.speed<.015)
    }
}


