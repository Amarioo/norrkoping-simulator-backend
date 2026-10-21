package com.norrkoping.simulator

import kotlin.math.hypot
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Same reference points as the UI's geo-reference-points.json (Lantmäteriet-published values). */
class Sweref99TmTest {
    private data class Ref(val lat: Double, val lon: Double, val e: Double, val n: Double, val tolerance: Double)
    private val dms = { d: Int, m: Int, s: Double -> d + m / 60.0 + s / 3600.0 }
    private val references = listOf(
        // Lantmäteriet SWEPOS calculation report, point G5TA (values printed to 0.1 mas / 1 mm).
        Ref(dms(60, 39, 59.47023), dms(17, 7, 56.56458), 616524.614, 6727534.361, 0.002),
        // Lantmäteriet STAC tile corners around Norrköping (dtm-cog catalogue footprints).
        Ref(58.54634930783993, 16.030843496253457, 560000.0, 6490000.0, 0.001),
        Ref(58.63464848877832, 16.205695414411846, 570000.0, 6500000.0, 0.001),
        Ref(58.54313334876391, 16.374358339591005, 580000.0, 6490000.0, 0.001),
        Ref(58.72443948868819, 16.20879941017453, 570000.0, 6510000.0, 0.001),
    )

    @Test fun `forward and inverse match Lantmateriet reference points`() {
        for (r in references) {
            val (e, n) = Sweref99Tm.forward(r.lat, r.lon)
            assertTrue(hypot(e - r.e, n - r.n) < r.tolerance, "forward $r -> $e,$n")
            val (lat, lon) = Sweref99Tm.inverse(r.e, r.n)
            // 1e-8 degrees is about 1 mm.
            assertEquals(r.lat, lat, 2e-8)
            assertEquals(r.lon, lon, 2e-8)
        }
    }

    @Test fun `bundled terrain uses the SWEREF 99 TM scene contract`() {
        val t = TerrainService.bundled
        val r = t.data.reference
        assertEquals("EPSG:3006", r.crs)
        assertEquals(569030.0, r.originEasting)
        assertEquals(6494989.0, r.originNorthing)
        val (x0, z0) = t.fromSweref(569030.0, 6494989.0)
        assertEquals(0.0, x0, 1e-12)
        assertEquals(0.0, z0, 1e-12)
        // 1 km of grid easting is 1 km / gridScaleFactor of ground, i.e. 40.0137 scene units.
        val (x1, _) = t.fromSweref(570030.0, 6494989.0)
        assertEquals(1000.0 / 25.0 / r.gridScaleFactor, x1, 1e-9)
        val (lat, lon) = t.unproject(12.3, -45.6)
        val (x, z) = t.project(lat, lon)
        // Krüger series round trip: a few micrometres (1e-6 units = 25 µm).
        assertEquals(12.3, x, 1e-6)
        assertEquals(-45.6, z, 1e-6)
    }

    @Test fun `bundled network stays inside the map and keeps edge lengths consistent`() {
        val network = TransportNetwork.bundled
        assertTrue(network.nodes.size > 30000)
        for (e in network.edges.take(5000)) {
            val a = network.nodes[e.from]
            val b = network.nodes[e.to]
            assertEquals(hypot(b.x - a.x, b.z - a.z), e.length, 0.0011)
        }
        // Norrköping C platform area in SWEREF 99 TM scene coordinates (geodetic 58.5966 N, 16.1835 E).
        val (sx, sz) = TerrainService.bundled.project(58.5966, 16.1835)
        assertTrue(network.nodes.any { hypot(it.x - sx, it.z - sz) < 4.0 })
    }
}
