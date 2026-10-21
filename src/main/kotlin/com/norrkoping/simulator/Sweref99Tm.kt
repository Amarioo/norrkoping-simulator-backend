package com.norrkoping.simulator

import kotlin.math.*

/**
 * SWEREF 99 TM (EPSG:3006): Gauss–Krüger projection of GRS 80, central meridian 15° E, scale 0.9996,
 * false easting 500 000 m. Lantmäteriet's Krüger series; same implementation as the UI's
 * src/lib/geo/sweref99tm.js (verified against Lantmäteriet SWEPOS point G5TA, Lantmäteriet STAC tile
 * corners and PROJ, see the UI's scripts/geo/verify_projection.py).
 */
object Sweref99Tm {
    private const val A = 6378137.0
    private const val F = 1 / 298.257222101
    const val CENTRAL_MERIDIAN = 15.0
    const val K0 = 0.9996
    const val FALSE_EASTING = 500000.0
    private val E2 = F * (2 - F)
    private val N = F / (2 - F)
    private val AROOF = A / (1 + N) * (1 + N.pow(2) / 4 + N.pow(4) / 64)
    private val CA = E2
    private val CB = (5 * E2.pow(2) - E2.pow(3)) / 6
    private val CC = (104 * E2.pow(3) - 45 * E2.pow(4)) / 120
    private val CD = 1237 * E2.pow(4) / 1260
    private val BETA = doubleArrayOf(
        N / 2 - 2 * N.pow(2) / 3 + 5 * N.pow(3) / 16 + 41 * N.pow(4) / 180,
        13 * N.pow(2) / 48 - 3 * N.pow(3) / 5 + 557 * N.pow(4) / 1440,
        61 * N.pow(3) / 240 - 103 * N.pow(4) / 140,
        49561 * N.pow(4) / 161280,
    )
    private val DELTA = doubleArrayOf(
        N / 2 - 2 * N.pow(2) / 3 + 37 * N.pow(3) / 96 - N.pow(4) / 360,
        N.pow(2) / 48 + N.pow(3) / 15 - 437 * N.pow(4) / 1440,
        17 * N.pow(3) / 480 - 37 * N.pow(4) / 840,
        4397 * N.pow(4) / 161280,
    )
    private val AS = E2 + E2.pow(2) + E2.pow(3) + E2.pow(4)
    private val BS = -(7 * E2.pow(2) + 17 * E2.pow(3) + 30 * E2.pow(4)) / 6
    private val CS = (224 * E2.pow(3) + 889 * E2.pow(4)) / 120
    private val DS = -4279 * E2.pow(4) / 1260

    /** Geodetic degrees → (easting, northing) metres. */
    fun forward(lat: Double, lon: Double): Pair<Double, Double> {
        val phi = Math.toRadians(lat)
        val dl = Math.toRadians(lon - CENTRAL_MERIDIAN)
        val s = sin(phi)
        val phis = phi - s * cos(phi) * (CA + CB * s.pow(2) + CC * s.pow(4) + CD * s.pow(6))
        val xi = atan2(tan(phis), cos(dl))
        val eta = atanh(cos(phis) * sin(dl))
        var x = xi
        var y = eta
        for (j in 1..4) {
            x += BETA[j - 1] * sin(2 * j * xi) * cosh(2 * j * eta)
            y += BETA[j - 1] * cos(2 * j * xi) * sinh(2 * j * eta)
        }
        return K0 * AROOF * y + FALSE_EASTING to K0 * AROOF * x
    }

    /** (easting, northing) metres → geodetic (lat, lon) degrees. */
    fun inverse(easting: Double, northing: Double): Pair<Double, Double> {
        val xi = northing / (K0 * AROOF)
        val eta = (easting - FALSE_EASTING) / (K0 * AROOF)
        var xip = xi
        var etap = eta
        for (j in 1..4) {
            xip -= DELTA[j - 1] * sin(2 * j * xi) * cosh(2 * j * eta)
            etap -= DELTA[j - 1] * cos(2 * j * xi) * sinh(2 * j * eta)
        }
        val phis = asin(sin(xip) / cosh(etap))
        val dl = atan(sinh(etap) / cos(xip))
        val s = sin(phis)
        val phi = phis + s * cos(phis) * (AS + BS * s.pow(2) + CS * s.pow(4) + DS * s.pow(6))
        return Math.toDegrees(phi) to CENTRAL_MERIDIAN + Math.toDegrees(dl)
    }
}
