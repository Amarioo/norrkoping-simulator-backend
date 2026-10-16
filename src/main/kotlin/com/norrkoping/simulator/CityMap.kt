package com.norrkoping.simulator

/**
 * Norrköping ground plan, matching the UI's OpenStreetMap snapshot (2026-10-16).
 * One scene unit = 25 geographical metres. X is east; Z is south.
 * x = (lon - 16.18747) * 58010 / 25
 * z = (58.58981 - lat) * 111133 / 25
 * Building/park destinations use mapped footprint centres; district targets are approximate.
 * Flight physics remain in simulation units, independently of the miniature's map scale.
 */
object CityMap {
    data class Place(val name: String, val x: Double, val z: Double)

    /** Fictional simulation depots; IDs retained for API compatibility. */
    val hubs = listOf(
        HubView("HUB_HAMNEN", "Hamndepån (simulerad)", listOf(60.0, 0.0, -56.0)),
        HubView("HUB_VRINNEVI", "Söderdepån (simulerad)", listOf(-18.0, 0.0, 62.0)),
    )

    val places = listOf(
        Place("Drottninggatan", 0.0, 4.8),
        Place("Gamla torget", -3.822, 4.965),
        Place("Rådhustorget", -0.734, -9.874),
        Place("Sankt Olai kyrka", 4.516, -1.094),
        Place("Arbetets museum", -17.428, 3.043),
        Place("Louis De Geer-hallen", -8.477, 7.283),
        Place("Visualiseringscentret", -25.725, 4.774),
        Place("Campus Norrköping", -24.593, -0.438),
        Place("Carl Johans park", -7.752, -21.652),
        Place("Norrköping C", -9.018, -29.373),
        Place("Saltängen", 19.584, -27.517),
        Place("Söder Tull", 3.0, 17.4),
        Place("Sankt Johannes kyrka", 22.775, 58.516),
        Place("Himmelstalund", -70.0, -12.0),
        Place("Nya Parken", -33.65, 24.7),
        Place("Norrköpings hamn", 58.0, -28.0),
        Place("Östra promenaden", 16.5, -1.8),
        Place("Ljuraparken", 34.880, 40.294),
        Place("Ljuraskolan", 29.631, 30.084),
        Place("Konstmuseet", 7.837, 30.718),
        Place("Centralbadet", 35.157, 24.355),
        Place("Värmekyrkan", -15.085, 6.175),
        Place("Matteus kyrka", -36.413, -14.538),
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
