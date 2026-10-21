package com.norrkoping.simulator

import kotlin.math.*
import java.util.PriorityQueue

/** Fictional shuttle using mapped runway headings and taxiway polylines. */
class RegionalFlight(private val network: TransportNetwork = TransportNetwork.bundled, private val terrain: TerrainService = TerrainService.bundled) {
    /** Ground positions: 0.01 above the runway pavement, which was the legacy level 0.09 (mapLandmarks.ts). */
    private fun point(p: List<Double>,y: Double=.1)=Vec3(p[0],terrain.placeLegacy(p[0],p[1],y,RUNWAY_SURFACE),p[1])
    private companion object { const val RUNWAY_SURFACE = .09 }
    private data class Airfield(val airport: Airport,val start: Vec3,val end: Vec3,val direction: Vec3,val gate: Vec3,val taxiOut: List<Vec3>,val taxiIn: List<Vec3>,val exit: Vec3)
    private fun airfield(a: Airport): Airfield {
        val start=point(a.start);val end=point(a.end);val direction=(end-start).normalized();val runwayLength=(end-start).length()
        val ways=network.airways.filter { it.kind=="taxiway" && (point(it.points.first())-start).length()<100 }
        val points=ways.flatMap { it.points }.map { point(it) }.distinct();require(points.isNotEmpty())
        val index=points.withIndex().associate { it.value to it.index };val links=mutableMapOf<Int,MutableList<Pair<Int,Double>>>()
        for(w in ways)for(i in 1 until w.points.size){val x=index.getValue(point(w.points[i-1]));val y=index.getValue(point(w.points[i]));val d=(points[x]-points[y]).length();links.getOrPut(x){mutableListOf()}.add(y to d);links.getOrPut(y){mutableListOf()}.add(x to d)}
        val unseen=points.indices.toMutableSet();val components=mutableListOf<Set<Int>>()
        while(unseen.isNotEmpty()){val component=mutableSetOf<Int>();val queue=ArrayDeque<Int>();queue.add(unseen.first());while(queue.isNotEmpty()){val n=queue.removeFirst();if(!unseen.remove(n))continue;component.add(n);links[n].orEmpty().forEach { queue.add(it.first) }};components.add(component)}
        val connected=components.maxBy { it.size }
        fun nearest(p: Vec3)=connected.minBy { (points[it]-p).length() }
        val mid=(start+end)*.5
        val gate=connected.minBy { val p=points[it];abs((p-mid).dot(direction)) + abs(abs((p-start).x*direction.z-(p-start).z*direction.x)-2.5)*2 }
        val threshold=nearest(start)
        val exitIndex=connected.filter { val rel=points[it]-start; val t=rel.dot(direction)/runwayLength; t in .45..0.95 && abs(rel.x*direction.z-rel.z*direction.x)<a.width*.55 }.minByOrNull { abs((points[it]-start).dot(direction)/runwayLength-.65) } ?: nearest(start+(end-start)*.65)
        val exit=start+direction*((points[exitIndex]-start).dot(direction).coerceIn(runwayLength*.45,runwayLength*.95))
        fun route(from: Int,to: Int): List<Vec3> {
            val distance=DoubleArray(points.size){Double.POSITIVE_INFINITY};val previous=IntArray(points.size){-1};val queue=PriorityQueue<Pair<Int,Double>>(compareBy { it.second });distance[from]=0.0;queue.add(from to 0.0)
            while(queue.isNotEmpty()){val (u,cost)=queue.remove();if(cost>distance[u])continue;if(u==to)break;for((v,d) in links[u].orEmpty()){if(cost+d<distance[v]){distance[v]=cost+d;previous[v]=u;queue.add(v to distance[v])}}}
            require(distance[to].isFinite()) { "Disconnected taxiway at ${a.id}" }
            val path=mutableListOf<Vec3>();var node=to;while(node!=-1){path.add(points[node]);if(node==from)break;node=previous[node]};return path.reversed()
        }
        return Airfield(a,start,end,direction,points[gate],(route(gate,threshold)+start).distinct(),(listOf(exit)+route(exitIndex,gate)).distinct(),exit)
    }
    private val fields=network.airports.map(::airfield)
    private var origin=0
    var phase="PARKED";private set
    private var elapsed=0.0
    var completed=0;private set
    private var lastPosition=fields[0].gate
    private var previousHeading=atan2(fields[0].direction.x,fields[0].direction.z)
    private var view=AircraftView(pos=lastPosition.array(),heading=previousHeading,pitch=0.0,bank=0.0,phase=phase,origin=fields[0].airport.name,destination=fields[1].airport.name,speed=0.0,gear=true)
    private fun length(path:List<Vec3>)=path.zipWithNext().sumOf { (a,b)->(b-a).length() }
    private fun along(path:List<Vec3>,t:Double):Vec3 {var dist=length(path)*t.coerceIn(0.0,1.0);for((a,b) in path.zipWithNext()){val l=(b-a).length();if(dist<=l)return a+(b-a)*(dist/max(l,.0001));dist-=l};return path.last()}
    private fun bezier(a:Vec3,b:Vec3,c:Vec3,d:Vec3,t:Double)=a*((1-t).pow(3))+b*(3*(1-t).pow(2)*t)+c*(3*(1-t)*t*t)+d*(t*t*t)
    private fun endpoints(): Pair<Vec3,Vec3> {val from=fields[origin];val to=fields[1-origin];val a=from.end+from.direction*((from.end-from.start).length()*.2);val b=to.start-to.direction*85.0;return Vec3(a.x,8.0,a.z) to Vec3(b.x,8.0,b.z)}
    private fun duration():Double {val from=fields[origin];val to=fields[1-origin];return when(phase){"PARKED"->10.0;"TAXI_OUT"->length(from.taxiOut)/.18;"TAKEOFF"->35.0;"CLIMB"->25.0;"CRUISE"->{val (a,b)=endpoints();max(35.0,(b-a).length()/2.8)};"APPROACH"->38.0;"LANDING"->20.0;"TAXI_IN"->length(to.taxiIn)/.18;else->1.0}.coerceAtLeast(.1)}
    private fun position(t:Double):Vec3 {
        val from=fields[origin];val to=fields[1-origin];val launch=from.start+(from.end-from.start)*.65;val (a,b)=endpoints();val touch=to.start+(to.end-to.start)*.12
        return when(phase){
            "PARKED"->from.gate
            "TAXI_OUT"->along(from.taxiOut,t)
            "TAKEOFF"->{val p=from.start+(launch-from.start)*(t*t);Vec3(p.x,p.y+max(0.0,(t-.78)/.22).pow(2)*1.3,p.z)}
            "CLIMB"->{val p=Vec3(launch.x,launch.y+1.3,launch.z)+(a-Vec3(launch.x,launch.y+1.3,launch.z))*t;p}
            "CRUISE"->bezier(a,a+from.direction*65.0,b-to.direction*65.0,b,t)
            "APPROACH"->b+(touch-b)*t
            "LANDING"->touch+(to.exit-touch)*(1-(1-t).pow(2))
            "TAXI_IN"->along(to.taxiIn,t)
            else->from.gate
        }
    }
    fun tick(dt:Double): Boolean {
        elapsed+=dt;var landed=false
        while(elapsed>=duration()) {elapsed-=duration();phase=when(phase){"PARKED"->"TAXI_OUT";"TAXI_OUT"->"TAKEOFF";"TAKEOFF"->"CLIMB";"CLIMB"->"CRUISE";"CRUISE"->"APPROACH";"APPROACH"->{completed++;landed=true;"LANDING"};"LANDING"->"TAXI_IN";else->{origin=1-origin;"PARKED"}}}
        val t=(elapsed/duration()).coerceIn(0.0,1.0);val p=position(t);val tangent=position(min(1.0,t+.002))-position(max(0.0,t-.002))
        val heading=if(tangent.length()>.0001)atan2(tangent.x,tangent.z) else previousHeading
        val turn=atan2(sin(heading-previousHeading),cos(heading-previousHeading))
        val airborne=phase in setOf("CLIMB","CRUISE","APPROACH")
        view=AircraftView(pos=p.array(),heading=heading,pitch=if(airborne)atan2(tangent.y,hypot(tangent.x,tangent.z)) else if(phase=="TAKEOFF"&&t>.78).08 else 0.0,bank=if(airborne)(-turn/max(dt,.001)*1.2).coerceIn(-.35,.35) else 0.0,phase=phase,origin=fields[origin].airport.name,destination=fields[1-origin].airport.name,speed=(p-lastPosition).length()/max(dt,.001)*25*3.6,gear=phase !in setOf("CLIMB","CRUISE"))
        previousHeading=heading;lastPosition=p;return landed
    }
    fun view()=view
}
