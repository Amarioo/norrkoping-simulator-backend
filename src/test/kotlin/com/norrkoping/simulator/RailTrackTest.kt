package com.norrkoping.simulator

import kotlin.math.*
import kotlin.test.*
import kotlinx.serialization.json.*
import org.junit.jupiter.api.Test

/**
 * Batch A (railway alignment): rail vehicles use the UI's rail track contract (trackProfile.ts). The
 * fixture holds RouteTrack.pose() section poses from the UI (scripts/geo/infra-profile-control.test.ts),
 * including every deck node, where the old rule floated trains up to 7 m off the drawn deck.
 */
class RailTrackTest {
    private val terrain = TerrainService.bundled
    private val fixture = Json.parseToJsonElement(
        checkNotNull(RailTrackTest::class.java.getResourceAsStream("/rail-track-control.json")).bufferedReader().use { it.readText() },
    ).jsonObject

    @Test fun usesTheSameRevisionAndVehicleGeometry() {
        assertEquals(fixture.getValue("revision").jsonPrimitive.content, terrain.data.revision)
        val head = fixture.getValue("railHeadMetres").jsonObject
        assertEquals(head.getValue("rail").jsonPrimitive.double, RailContract.RAIL_HEAD_METRES.getValue("train"))
        assertEquals(head.getValue("tram").jsonPrimitive.double, RailContract.RAIL_HEAD_METRES.getValue("tram"))
        val vehicles = fixture.getValue("vehicles").jsonObject
        for (kind in listOf("train", "tram")) {
            val v = vehicles.getValue(kind).jsonObject
            assertEquals(v.getValue("bogieHalf").jsonPrimitive.double, RailContract.BOGIE_HALF_METRES.getValue(kind))
            assertEquals(v.getValue("sectionSpacing").jsonPrimitive.double, RailContract.SECTION_SPACING_METRES.getValue(kind))
            assertEquals(v.getValue("sections").jsonPrimitive.int, RailContract.SECTIONS.getValue(kind))
        }
    }

    @Test fun reproducesTheUiSectionPoses() {
        val routes = TransportNetwork.bundled.transitRoutes.associateBy { it.id }
        var checked = 0
        for ((id, rows) in fixture.getValue("routes").jsonObject) {
            val track = RailTrack(routes.getValue(id), terrain)
            for (row in rows.jsonArray) {
                val v = row.jsonArray.map { it.jsonPrimitive.double }
                val pose = track.pose(v[0])
                for (axis in 0..2) assertEquals(v[1 + axis], pose.pos[axis], 1e-7, "$id ${v[0]} axis $axis")
                assertEquals(0.0, atan2(sin(v[4] - pose.heading), cos(v[4] - pose.heading)), 1e-7, "$id heading")
                assertEquals(v[5], pose.pitch, 1e-7, "$id pitch")
                checked++
            }
        }
        assertTrue(checked > 900)
    }

    @Test fun sectionsRestOnTheirBogies() {
        val rail = RailTraffic(); rail.configure(TrafficSettings(trams = 6, trains = 3))
        repeat(40) { rail.tick(.5) }
        val tracks = TransportNetwork.bundled.transitRoutes.associate { it.name to RailTrack(it, terrain) }
        val coarse = tracks.mapValues { (_, t) -> (0 until (t.length / .05).toInt()).map { k -> k * .05 to t.position(k * .05) } }
        for (v in rail.views()) {
            val track = tracks.getValue(v.road)
            assertEquals(RailContract.SECTIONS.getValue(v.kind), v.sections.size)
            for (s in v.sections) {
                // Bogie centres reconstructed from the pose lie on the rail top of the track.
                val half = track.bogieHalf
                val reach = half * cos(s.pitch)
                for (sign in listOf(1.0, -1.0)) {
                    val x = s.pos[0] + sign * reach * sin(s.heading); val z = s.pos[2] + sign * reach * cos(s.heading)
                    val y = s.pos[1] + sign * half * sin(s.pitch)
                    // Nearest track point: coarse table, then refined around the best candidates.
                    var best = Double.POSITIVE_INFINITY; var bestY = 0.0
                    for ((d0, _) in coarse.getValue(v.road).sortedBy { (_, p) -> hypot(p[0] - x, p[2] - z) }.take(6)) {
                        var d = d0 - .06
                        while (d <= d0 + .06) {
                            val p = track.position(d)
                            val gap = hypot(p[0] - x, p[2] - z)
                            if (gap < best) { best = gap; bestY = p[1] }
                            d += .0005
                        }
                    }
                    assertTrue(best < .004, "bogie off the track by ${best * 25} m")
                    assertEquals(bestY, y, .004, "${v.kind} bogie height")
                }
            }
        }
    }
}
