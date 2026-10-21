package com.norrkoping.simulator

import kotlin.math.*

/**
 * Rail track contract, mirrored from the UI's src/lib/trackProfile.ts and verified against its fixture
 * (test resources rail-track-control.json, RailTrackTest.kt). Transit route points are the drawn track's
 * nodes, carrying the node's own network reference key, so vehicles use exactly the drawn rails'
 * height rules (deck profiles, engineered spans, approaches, terrain). The rail head above the drawn
 * running surface is a per-kind offset; vehicles rest on their two bogies (pitch follows the track).
 */
object RailContract {
    /** Top of rail above the drawn running surface, metres (ballasted mainline / embedded tram rail). */
    val RAIL_HEAD_METRES = mapOf("train" to .18, "tram" to .0)
    /** Bogie centres either side of the section centre, metres (X61 car 19.5 m / tram module 6.12 m). */
    val BOGIE_HALF_METRES = mapOf("train" to 6.24, "tram" to 1.8)
    /** Section centre spacing, metres. */
    val SECTION_SPACING_METRES = mapOf("train" to 20.0, "tram" to 6.4)
    val SECTIONS = mapOf("train" to 4, "tram" to 5)
    /** Drawn running surface above the network reference (terrain.ts ROAD_SURFACE), scene units. */
    const val ROAD_SURFACE = TerrainService.LEGACY_STREET - TerrainService.LEGACY_ROAD
}

/** A transit route as a continuous track: rail-top position and bogie-based pose by distance (scene units). */
class RailTrack(val route: TransitRoute, private val terrain: TerrainService) {
    private val unit = terrain.data.reference.metresPerUnit
    val lift = RailContract.ROAD_SURFACE + RailContract.RAIL_HEAD_METRES.getValue(route.kind) / unit
    val bogieHalf = RailContract.BOGIE_HALF_METRES.getValue(route.kind) / unit
    val sectionSpacing = RailContract.SECTION_SPACING_METRES.getValue(route.kind) / unit
    val cumulative = DoubleArray(route.points.size).also { c ->
        for (i in 1 until c.size) c[i] = c[i - 1] + hypot(route.points[i][0] - route.points[i - 1][0], route.points[i][2] - route.points[i - 1][2])
    }
    val length get() = cumulative.last()
    /** Segment index and parameter at a distance wrapped onto the loop (same search as trackProfile.ts). */
    fun locate(distance: Double): Pair<Int, Double> {
        val d = ((distance % length) + length) % length
        var lo = 0; var hi = cumulative.size - 1
        while (hi - lo > 1) { val mid = (lo + hi) ushr 1; if (cumulative[mid] <= d) lo = mid else hi = mid }
        val i = lo.coerceAtMost(cumulative.size - 2)
        return i to ((d - cumulative[i]) / (cumulative[i + 1] - cumulative[i]).coerceAtLeast(1e-5)).coerceIn(0.0, 1.0)
    }
    /** Rail-top point [x, y, z] at a distance. */
    fun position(distance: Double): List<Double> {
        val (i, t) = locate(distance)
        val a = route.points[i]; val b = route.points[i + 1]
        return listOf(a[0] + (b[0] - a[0]) * t, terrain.profileHeight(a, b, t) + lift, a[2] + (b[2] - a[2]) * t)
    }
    /** Both bogie centres on the rail top; the body centre on the chord between them. */
    fun pose(distance: Double): VehicleSection {
        val f = position(distance + bogieHalf); val r = position(distance - bogieHalf)
        val dx = f[0] - r[0]; val dz = f[2] - r[2]
        return VehicleSection(listOf((f[0] + r[0]) / 2, (f[1] + r[1]) / 2, (f[2] + r[2]) / 2), atan2(dx, dz), atan2(f[1] - r[1], hypot(dx, dz)))
    }
}

/** Rail vehicles follow continuous OSM polylines; each articulated section rests on its own two bogies. */
class RailTraffic(private val routes: List<TransitRoute> = TransportNetwork.bundled.transitRoutes, private val terrain: TerrainService = TerrainService.bundled) {
    private data class Unit(val id: Int,val track: RailTrack,var distance: Double,var speed: Double=0.0,var dwell: Double=0.0,var lastStop: String?=null)
    private val tracks=routes.map { RailTrack(it, terrain) }
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
        val track=unit.track
        val sections=List(RailContract.SECTIONS.getValue(track.route.kind)) { track.pose(unit.distance-it*track.sectionSpacing) }
        val front=sections.first()
        VehicleView(unit.id,track.route.kind,front.pos,front.heading,unit.speed*90,track.route.name,unit.speed<.015,sections,if(unit.dwell>0)unit.lastStop else null, track.route.destinations.lastOrNull { it.distance <= unit.distance }?.name, front.pitch)
    }
}
