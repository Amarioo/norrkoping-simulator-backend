package com.swarm

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class CityMapTest {
    @Test fun `Parken aliases resolve southwest and Ljura southeast of the centre`() {
        val stadium = requireNotNull(CityMap.place("Idrottsparken"))
        assertEquals(stadium, CityMap.place(" PARKEN "))
        assertEquals(stadium, CityMap.place("Nya Parken"))
        assertTrue(stadium.x < -30 && stadium.z > 20)
        val ljura = requireNotNull(CityMap.place("Ljuraparken"))
        assertEquals(34.880, ljura.x, 0.001)
        assertEquals(40.294, ljura.z, 0.001)
        assertTrue(CityMap.places.all { it.x in -79.0..79.0 && it.z in -79.0..79.0 })
    }
}
