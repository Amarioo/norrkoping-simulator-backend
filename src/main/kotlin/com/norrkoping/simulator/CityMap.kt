package com.norrkoping.simulator

/**
 * Norrköping ground plan, matching the UI's OpenStreetMap snapshot (2026-10-16).
 * One scene unit = 25 ground metres. X is east (SWEREF 99 TM grid east); Z is south.
 * x = (E - 569030) / (25 * 0.9996584126), z = (6494989 - N) / (25 * 0.9996584126)
 * with (E, N) in SWEREF 99 TM (EPSG:3006); see TerrainService.GeoReference.
 * Values were migrated on 2026-10-18 from the legacy equirectangular frame (same geographic points).
 * Building/park destinations use mapped footprint centres; district targets are approximate.
 * Flight physics remain in simulation units, independently of the miniature's map scale.
 */
object CityMap {
    data class Place(val name: String, val x: Double, val z: Double)

    /** Fictional simulation depots; IDs retained for API compatibility. */
    val hubs = listOf(
        HubView("HUB_HAMNEN", "Hamndepån (simulerad)", listOf(59.139, 0.0, -57.2)),
        HubView("HUB_VRINNEVI", "Söderdepån (simulerad)", listOf(-16.94, 0.0, 62.445)),
    )

    val places = listOf(
        Place("Drottninggatan", 0.096, 4.805),
        Place("Gamla torget", -3.732, 5.038),
        Place("Rådhustorget", -0.9, -9.887),
        Place("Sankt Olai kyrka", 4.518, -1.182),
        Place("Arbetets museum", -17.405, 3.352),
        Place("Louis De Geer-hallen", -8.357, 7.443),
        Place("Visualiseringscentret", -25.691, 5.233),
        Place("Campus Norrköping", -24.648, -0.01),
        Place("Carl Johans park", -8.142, -21.566),
        Place("Norrköping C", -9.548, -29.281),
        Place("Saltängen", 19.15, -27.93),
        Place("Söder Tull", 3.327, 17.379),
        Place("Sankt Johannes kyrka", 23.886, 58.23),
        Place("Himmelstalund", -70.363, -10.805),
        Place("Nya Parken", -33.287, 25.341),
        Place("Norrköpings hamn", 57.642, -29.104),
        Place("Östra promenaden", 16.518, -2.103),
        Place("Ljuraparken", 35.698, 39.752),
        Place("Ljuraskolan", 30.252, 29.615),
        Place("Konstmuseet", 8.413, 30.639),
        Place("Centralbadet", 35.689, 23.774),
        Place("Värmekyrkan", -15.001, 6.45),
        Place("Matteus kyrka", -36.743, -13.933),
    )

    /** Case-insensitive lookup; Parken aliases preserve old clients. */
    fun place(name: String?): Place? = name?.let { value ->
        val wanted = when (value.trim().lowercase()) {
            "idrottsparken", "parken" -> "Nya Parken"
            else -> value.trim()
        }
        places.firstOrNull { it.name.equals(wanted, ignoreCase = true) }
    }
}
