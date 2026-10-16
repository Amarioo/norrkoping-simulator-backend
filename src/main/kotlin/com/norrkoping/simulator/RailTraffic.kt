package com.norrkoping.simulator

import kotlin.math.*

/** Rail vehicles follow continuous OSM polylines; each articulated section samples its own pose. */
class RailTraffic(private val routes: List<TransitRoute> = TransportNetwork.bundled.transitRoutes) {
    private class Track(val route: TransitRoute) {
        val cumulative=DoubleArray(route.points.size)
        init { for(i in 1 until cumulative.size) cumulative[i]=cumulative[i-1]+hypot(route.points[i][0]-route.points[i-1][0],route.points[i][2]-route.points[i-1][2]) }
        val length get()=cumulative.last()
        fun position(distance: Double): List<Double> {
            val d=((distance%length)+length)%length
            var i=cumulative.binarySearch(d);if(i<0)i=(-i-2).coerceAtLeast(0);i=i.coerceAtMost(cumulative.size-2)
            val t=((d-cumulative[i])/(cumulative[i+1]-cumulative[i]).coerceAtLeast(.00001)).coerceIn(0.0,1.0)
            return List(3) { axis -> route.points[i][axis]+(route.points[i+1][axis]-route.points[i][axis])*t }
        }
        fun pose(distance: Double): VehicleSection {
            val a=position(distance-.06);val b=position(distance+.06)
            return VehicleSection(position(distance),atan2(b[0]-a[0],b[2]-a[2]))
        }
    }
    private data class Unit(val id: Int,val track: Track,var distance: Double,var speed: Double=0.0,var dwell: Double=0.0,var lastStop: String?=null)
    private val tracks=routes.map(::Track)
    private val units=mutableListOf<Unit>()
    private var nextId=10001
    private fun length(unit: Unit)=if(unit.track.route.kind=="train")3.2 else 1.28
    fun configure(settings: TrafficSettings) {
        for((kind,count) in listOf("tram" to settings.trams,"train" to settings.trains)) {
            val old=units.filter { it.track.route.kind==kind }
            if(old.size==count)continue
            units.removeAll(old.toSet())
            val available=tracks.filter { it.route.kind==kind }
            require(count==0||available.isNotEmpty()) { "Missing mapped $kind routes" }
            repeat(count) { i ->
                val track=available[i%available.size]
                val onTrack=(count+available.size-1-i%available.size)/available.size
                val progress=(i/available.size+.25)/onTrack*track.length
                units.add(Unit(nextId++,track,progress))
            }
        }
    }
    fun tick(dt: Double) {
        val previous=units.associate { it.id to it.distance }
        for(unit in units) {
            if(unit.dwell>0) { unit.dwell=max(0.0,unit.dwell-dt);unit.speed=0.0;continue }
            val track=unit.track
            var gap=Double.POSITIVE_INFINITY
            for(other in units)if(other!==unit&&other.track===track) {
                val ahead=((previous.getValue(other.id)-previous.getValue(unit.id))%track.length+track.length)%track.length
                gap=min(gap,ahead-length(other)-.25)
            }
            val stop=track.route.stops.map { it to ((it.distance-unit.distance)%track.length+track.length)%track.length }.filter { it.second>.005 }.minByOrNull { it.second }
            val stopDistance=stop?.second?:Double.POSITIVE_INFINITY
            val limit=if(track.route.kind=="train")1.35 else .50
            val desired=min(limit,min(sqrt(max(0.0,2*.045*(stopDistance-.01))),max(0.0,gap/1.8)))
            unit.speed+=(desired-unit.speed).coerceIn(-.08*dt,.032*dt)
            val distance=min(unit.speed*dt,max(0.0,gap))
            if(stop!=null && stopDistance<max(.025,distance)) {
                unit.distance=(stop.first.distance+.006)%track.length
                unit.dwell=if(track.route.kind=="train")35.0 else 14.0
                unit.lastStop=stop.first.name;unit.speed=0.0
            }else unit.distance=(unit.distance+distance)%track.length
        }
    }
    fun views(): List<VehicleView> = units.map { unit ->
        val train=unit.track.route.kind=="train"
        val sections=List(if(train)4 else 5) { unit.track.pose(unit.distance-it*(if(train).8 else .256)) }
        val front=sections.first()
        VehicleView(unit.id,unit.track.route.kind,front.pos,front.heading,unit.speed*90,unit.track.route.name,unit.speed<.015,sections,if(unit.dwell>0)unit.lastStop else null)
    }
}
