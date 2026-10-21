package com.norrkoping.simulator

import kotlin.test.*
import kotlinx.serialization.json.*
import org.junit.jupiter.api.Test

/**
 * Step 3: the backend surface is the UI's adaptive terrain mesh (surfaceMesh.ts), so vehicles ride on
 * the drawn ground. The fixture holds mesh heights from the reference implementation
 * (UI scripts/geo/rtin.py, written by scripts/geo/terrain_mesh_samples.py).
 */
class TerrainMeshTest {
    private val terrain = TerrainService.bundled
    private val fixture = Json.parseToJsonElement(
        checkNotNull(TerrainMeshTest::class.java.getResourceAsStream("/terrain-mesh-control.json")).bufferedReader().use { it.readText() },
    ).jsonObject

    @Test fun matchesTheReferenceMeshHeights() {
        val points = fixture.getValue("mesh").jsonArray
        assertTrue(points.size >= 1000)
        for (p in points) {
            val (x, z, metres) = p.jsonArray.map { it.jsonPrimitive.double }
            assertEquals(metres, terrain.surfaceHeight(x, z) * 25, 2e-6)
            assertEquals(terrain.surfaceHeight(x, z), terrain.groundHeight(x, z), 1e-12)
        }
    }

    @Test fun isContinuousAndPlanarBetweenNodes() {
        val points = fixture.getValue("mesh").jsonArray.take(300)
        for (p in points) {
            val (x, z) = p.jsonArray.map { it.jsonPrimitive.double }
            // Tiny steps never jump: the mesh has no cracks (5 m nodes, slopes below 100 %).
            for ((dx, dz) in listOf(1e-7 to 0.0, 0.0 to 1e-7, 1e-7 to 1e-7))
                assertTrue(kotlin.math.abs(terrain.surfaceHeight(x + dx, z + dz) - terrain.surfaceHeight(x - dx, z - dz)) * 25 < 1e-4)
        }
    }
}
