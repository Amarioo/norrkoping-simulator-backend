package com.norrkoping.simulator

import kotlin.test.*
import kotlinx.serialization.json.*
import org.junit.jupiter.api.Test

/** Step 2: the legacy flat-scene layer lift is gone from network heights (same rule as terrain.ts). */
class ElevationLayersTest {
    private val terrain = TerrainService.bundled
    private val network = TransportNetwork.bundled
    private val drop = TerrainService.LEGACY_STREET - TerrainService.LEGACY_GROUND
    private val profiles = Json.parseToJsonElement(
        checkNotNull(TerrainService::class.java.getResourceAsStream("/bridge-profiles.json")).bufferedReader().use { it.readText() },
    ).jsonObject.getValue("points").jsonObject

    @Test fun ordinaryRoadSurfaceIsTheTerrain() {
        val nodes = network.nodes.filter { it.y == .071 && terrain.bridgeHeight(it.x, it.z, it.y) == null }.take(500)
        assertTrue(nodes.size == 500)
        for (n in nodes) {
            // Reference + drawn road surface (0.006) = rendered DTM surface; cars ride 0.002 (5 cm) above it.
            assertEquals(terrain.groundHeight(n.x, n.z), terrain.roadHeight(n.x, n.z, n.y) + .006, 1e-12)
            val car = terrain.profileHeight(listOf(n.x, n.y, n.z), listOf(n.x, n.y, n.z), 0.0) + .008
            assertEquals(.05, (car - terrain.groundHeight(n.x, n.z)) * 25, 1e-9)
        }
    }

    @Test fun bridgeProfilesMoveWithTheirApproachRoads() {
        val (key, value) = profiles.entries.first()
        val (x, z, legacy) = key.split(",").map { it.toDouble() }
        assertEquals(value.jsonPrimitive.double - drop, terrain.roadHeight(x, z, legacy), 1e-12)
        assertEquals(.097, drop, 1e-12)
    }

    @Test fun elevatedLiftsAboveTheRoadLevelAreKept() {
        val n = network.nodes.first { it.y == .071 && terrain.bridgeHeight(it.x, it.z, it.y) == null }
        assertEquals(.24, terrain.roadHeight(n.x, n.z, .311) - terrain.roadHeight(n.x, n.z, .071), 1e-12)
        // Flat-scene dips below the road level (Ingelsta cut) are in the DTM already.
        assertEquals(terrain.roadHeight(n.x, n.z, .071), terrain.roadHeight(n.x, n.z, .02), 1e-12)
    }

    @Test fun legacyModelsStandOnTheirSurface() {
        val n = network.nodes.first()
        val ground = terrain.groundHeight(n.x, n.z)
        // Aircraft (RegionalFlight): legacy 0.1 on the runway pavement modelled at 0.09.
        assertEquals(ground + .01, terrain.placeLegacy(n.x, n.z, .1, .09), 1e-12)
        assertEquals(3.04, terrain.placeLegacy(n.x, n.z, .1, .06, 3.0), 1e-12)
    }
}
