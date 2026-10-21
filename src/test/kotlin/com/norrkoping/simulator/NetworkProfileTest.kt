package com.norrkoping.simulator

import kotlin.test.*
import kotlinx.serialization.json.*
import org.junit.jupiter.api.Test

/**
 * Step 3.5 (network profile rules v4): vehicles use the same heights as the drawn network. The
 * fixture holds terrain.ts profileHeight() values from the UI (scripts/geo/infra-profile-control.test.ts)
 * for engineered spans over underpasses and culverts, bridge approaches (one profiled end, terrain with
 * tapered offsets), decks (both ends profiled, linear), ordinary edges and rail routes over spans.
 */
class NetworkProfileTest {
    private val terrain = TerrainService.bundled
    private val fixture = Json.parseToJsonElement(
        checkNotNull(NetworkProfileTest::class.java.getResourceAsStream("/network-profile-control.json")).bufferedReader().use { it.readText() },
    ).jsonObject

    @Test fun usesTheSameRevision() {
        assertEquals(fixture.getValue("revision").jsonPrimitive.content, terrain.data.revision)
    }

    @Test fun reproducesTheUiNetworkHeights() {
        val groups = fixture.getValue("groups").jsonObject
        // integrated: segments absorbed into the road breakline patches (Step 5), draped on the patch surface.
        for (group in listOf("span", "approach", "deck", "ordinary", "railSpan", "integrated")) {
            val rows = groups.getValue(group).jsonArray
            assertTrue(rows.size >= 8, group)
            for (row in rows) {
                val v = row.jsonArray.map { it.jsonPrimitive.double }
                val expected = v[7]
                assertEquals(expected, terrain.profileHeight(v.subList(0, 3), v.subList(3, 6), v[6]), 2e-7, "$group $v")
            }
        }
    }

    @Test fun spansAreLinearAndDecksNeedTwoProfiledEnds() {
        val span = fixture.getValue("groups").jsonObject.getValue("span").jsonArray.first().jsonArray.map { it.jsonPrimitive.double }
        // A span may cover only part of its segment.
        assertTrue((0..20).any { terrain.spanHeight(span[0], span[2], span[3], span[5], it / 20.0) != null })
        val approach = fixture.getValue("groups").jsonObject.getValue("approach").jsonArray.first().jsonArray.map { it.jsonPrimitive.double }
        assertFalse(terrain.linearBetween(approach.subList(0, 3), approach.subList(3, 6)))
        val deck = fixture.getValue("groups").jsonObject.getValue("deck").jsonArray.first().jsonArray.map { it.jsonPrimitive.double }
        assertTrue(terrain.linearBetween(deck.subList(0, 3), deck.subList(3, 6)))
    }
}
