package com.norrkoping.simulator

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlin.math.floor
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.util.Base64

/**
 * Horizontal scene contract (terrain dataset version 2), shared with the UI's terrain.ts:
 * scene = (SWEREF 99 TM - origin) / (metresPerUnit * gridScaleFactor); x east, z south.
 */
@Serializable data class GeoReference(
    val crs: String, val originEasting: Double, val originNorthing: Double,
    val gridScaleFactor: Double, val gridConvergenceDegrees: Double,
    val metresPerUnit: Double, val verticalDatum: String, val verticalOriginMetres: Double,
)
@Serializable data class TerrainTile(
    val id: String, val source: String, val sourceUrl: String, val licence: String,
    val acquired: String, val kind: String, val resolutionMetres: Double,
    val verticalAccuracyMetres: Double?, val verticalDatum: String, val processing: String,
    val x: Double, val z: Double, val step: Double, val columns: Int, val rows: Int,
    val heights: List<Double?>, val heightsBase64: String? = null,
)
/**
 * Rendered ground of a DTM revision: an adaptive RTIN over the tile's 5 m nodes (UI surfaceMesh.ts,
 * scripts/prepare-terrain-mesh.py). A triangle (right angle R, hypotenuse A-B, in nodes) is split at
 * the hypotenuse midpoint M when M's bit is set, into (M; R, A) and (M; B, R).
 */
@Serializable data class SurfaceMeshData(
    val kind: String, val tile: String, val x: Double, val z: Double, val step: Double,
    val depth: Int, val columns: Int, val rows: Int, val tolerancesMetres: Map<String, Double>,
    val errorReference: String, val triangles: Int, val activeBase64: String,
    val patches: SurfacePatches? = null,
)
/**
 * Road breakline patches (Step 5 prototype, UI scripts/geo/road_patches.py and surfaceMesh.ts): RTIN leaves
 * re-triangulated with the asphalt outline as mesh edges. Each patch triangle lies in one leaf.
 */
@Serializable data class SurfacePatches(
    val kind: String, val boxes: Map<String, List<Double>> = emptyMap(), val leaves: Int, val triangles: Int,
    val vertexCount: Int, val leavesBase64: String, val verticesBase64: String, val trianglesBase64: String,
    val method: String = "",
    /** Grounded linear road segments absorbed into the patches: draped on the surface (UI terrain.ts integrated()). */
    val integrated: List<String> = emptyList(),
    /** Step 5.1: diagnostic-driven patch groups (name to SWEREF box), UI scripts/geo/refinement_targets.py. */
    val groups: Map<String, List<Double>> = emptyMap(),
    /** Step 5.1: segments drawn on the measured surface of a patch (the rest crossing a patch keep the RTIN). */
    val members: List<String> = emptyList(),
    /** Step 5.1 optimisation: per triangle 0 terrain, 1 road asphalt, 2 track bed (drawing only; not used here). */
    val classesBase64: String = "",
)
@Serializable data class TerrainDataset(val version: Int, val revision: String, val reference: GeoReference, val tiles: List<TerrainTile>, val mesh: SurfaceMeshData? = null)
data class GroundSample(val sceneY: Double, val metresRH2000: Double, val sourceId: String, val resolutionMetres: Double, val quality: String)

/** Same grid contract and interpolation as the UI's terrain.ts. One vertical scale, no synthetic hills. */
class TerrainService(val data: TerrainDataset) {
    private val binary = data.tiles.mapNotNull { t -> t.heightsBase64?.let { encoded ->
        val bytes = Base64.getDecoder().decode(encoded)
        require(bytes.size.toLong() == t.columns.toLong()*t.rows*4)
        val values = FloatArray(t.columns*t.rows)
        ByteBuffer.wrap(bytes).order(ByteOrder.LITTLE_ENDIAN).asFloatBuffer().get(values)
        require(values.none { it.isInfinite() })
        t.id to values
    }}.toMap()
    private fun height(tile: TerrainTile, i: Int): Double? = if (tile.heightsBase64 != null) binary.getValue(tile.id)[i].toDouble().takeIf { it.isFinite() } else tile.heights[i]
    init {
        val r = data.reference
        require(data.version == DATASET_VERSION && data.revision.isNotBlank() && r.verticalDatum == "RH2000" && r.crs == "EPSG:3006")
        require(listOf(r.originEasting, r.originNorthing, r.gridConvergenceDegrees, r.verticalOriginMetres).all { it.isFinite() })
        require(listOf(r.metresPerUnit, r.gridScaleFactor).all { it.isFinite() && it > 0 })
        require(r.gridScaleFactor in 0.9995..1.002)
        require(data.tiles.map { it.id }.distinct().size == data.tiles.size)
        for (t in data.tiles) {
            require(listOf(t.id, t.source, t.sourceUrl, t.licence, t.acquired, t.processing).all { it.isNotBlank() })
            require(t.kind in listOf("DTM", "DSM-fallback", "water") && t.verticalDatum == r.verticalDatum)
            require(t.x.isFinite() && t.z.isFinite() && t.step.isFinite() && t.step > 0)
            require(t.resolutionMetres.isFinite() && t.resolutionMetres > 0)
            require(t.verticalAccuracyMetres == null || (t.verticalAccuracyMetres.isFinite() && t.verticalAccuracyMetres >= 0))
            require(t.columns >= 2 && t.rows >= 2 && t.columns.toLong() * t.rows == (binary[t.id]?.size ?: t.heights.size).toLong())
            require(t.heights.all { it == null || it.isFinite() })
        }
    }
    private val tiles = data.tiles.sortedWith(compareBy<TerrainTile> { it.kind != "DTM" }
        .thenBy { it.resolutionMetres }.thenBy { it.verticalAccuracyMetres ?: Double.POSITIVE_INFINITY }.thenBy { it.id })
    private val unit = data.reference.metresPerUnit * data.reference.gridScaleFactor
    /** SWEREF 99 TM (easting, northing) -> scene (x, z). */
    fun fromSweref(easting: Double, northing: Double): Pair<Double, Double> = with(data.reference) {
        (easting - originEasting) / unit to (originNorthing - northing) / unit
    }
    /** Scene (x, z) -> SWEREF 99 TM (easting, northing). */
    fun toSweref(x: Double, z: Double): Pair<Double, Double> = with(data.reference) {
        originEasting + x * unit to originNorthing - z * unit
    }
    /** Geodetic degrees -> scene (x, z). */
    fun project(lat: Double, lon: Double): Pair<Double, Double> = Sweref99Tm.forward(lat, lon).let { (e, n) -> fromSweref(e, n) }
    /** Scene (x, z) -> geodetic (lat, lon) degrees. */
    fun unproject(x: Double, z: Double): Pair<Double, Double> = toSweref(x, z).let { (e, n) -> Sweref99Tm.inverse(e, n) }
    fun toSceneHeight(metres: Double) = (metres - data.reference.verticalOriginMetres) / data.reference.metresPerUnit
    fun sample(x: Double, z: Double, water: Boolean = false): GroundSample? {
        require(x.isFinite() && z.isFinite())
        for (tile in tiles) {
            if ((tile.kind == "water") != water) continue
            val u = (x - tile.x) / tile.step; val v = (z - tile.z) / tile.step
            if (u < 0 || v < 0 || u > tile.columns - 1 || v > tile.rows - 1) continue
            val col = floor(u).toInt().coerceAtMost(tile.columns - 2)
            val row = floor(v).toInt().coerceAtMost(tile.rows - 2)
            val tx = u - col; val tz = v - row
            val indices = listOf(row*tile.columns+col, row*tile.columns+col+1, (row+1)*tile.columns+col, (row+1)*tile.columns+col+1)
            val weights = listOf((1-tx)*(1-tz), tx*(1-tz), (1-tx)*tz, tx*tz)
            if (indices.indices.any { weights[it] > 0 && height(tile, indices[it]) == null }) continue
            val metres = indices.indices.sumOf { (height(tile, indices[it]) ?: 0.0) * weights[it] }
            return GroundSample(toSceneHeight(metres), metres, tile.id, tile.resolutionMetres,
                if (tile.kind == "DTM") "measured-ground" else "surface-fallback")
        }
        return null
    }
    private val mesh = data.mesh?.takeIf { data.revision.startsWith("lm-dtm-") }
    private val meshTile = mesh?.let { m -> tiles.first { it.id == m.tile } }
    private val meshBits = mesh?.let { Base64.getDecoder().decode(it.activeBase64) }
    private val meshRoot = 1 shl (mesh?.depth ?: 0)
    init {
        require(data.revision.startsWith("lm-dtm-").not() || mesh != null) { "DTM revision without surface mesh" }
        mesh?.let { m ->
            val t = requireNotNull(meshTile)
            require(m.kind == "rtin" && t.kind == "DTM" && kotlin.math.abs(m.step - t.step) < 1e-12)
            require(m.depth in 1..12 && (m.columns - 1) % meshRoot == 0 && (m.rows - 1) % meshRoot == 0)
            require(meshBits!!.size == (m.columns.toLong() * m.rows + 7).div(8).toInt())
        }
    }
    /** Patched leaves: key -> (first triangle, count); vertices in node space with scene heights. */
    private val patchLeaves: Map<Int, Pair<Int, Int>>
    private val patchX: DoubleArray; private val patchZ: DoubleArray; private val patchY: DoubleArray
    private val patchTris: IntArray
    init {
        val p = mesh?.patches
        val dec = { text: String -> ByteBuffer.wrap(Base64.getDecoder().decode(text)).order(ByteOrder.LITTLE_ENDIAN) }
        if (p == null) { patchLeaves = emptyMap(); patchX = DoubleArray(0); patchZ = patchX; patchY = patchX; patchTris = IntArray(0) }
        else {
            val l = dec(p.leavesBase64); val v = dec(p.verticesBase64); val t = dec(p.trianglesBase64)
            require(l.remaining() == p.leaves * 12 && v.remaining() == p.vertexCount * 24 && t.remaining() == p.triangles * 12) { "Invalid surface patches" }
            patchLeaves = (0 until p.leaves).associate { l.getInt(it * 12) to (l.getInt(it * 12 + 4) to l.getInt(it * 12 + 8)) }
            patchX = DoubleArray(p.vertexCount) { v.getDouble(it * 24) }
            patchZ = DoubleArray(p.vertexCount) { v.getDouble(it * 24 + 8) }
            patchY = DoubleArray(p.vertexCount) { toSceneHeight(v.getDouble(it * 24 + 16)) }
            patchTris = IntArray(p.triangles * 3) { t.getInt(it * 4).also { i -> require(i in 0 until p.vertexCount) } }
        }
    }
    /** Same key as surfaceMesh.ts leafKey(): right-angle node and the direction to the hypotenuse. */
    private fun leafKey(rx: Int, rz: Int, ax: Int, az: Int, bx: Int, bz: Int) =
        (rz * mesh!!.columns + rx) * 9 + (Integer.signum(ax + bx - 2 * rx) + 1) * 3 + (Integer.signum(az + bz - 2 * rz) + 1)
    /** Height in a patched leaf: the triangle containing (gx, gz), as surfaceMesh.ts Patches.height(). */
    private fun patchHeight(leaf: Pair<Int, Int>, gx: Double, gz: Double): Double {
        var best = Double.NEGATIVE_INFINITY; var height = Double.NaN
        for (t in leaf.first until leaf.first + leaf.second) {
            val a = patchTris[t * 3]; val b = patchTris[t * 3 + 1]; val c = patchTris[t * 3 + 2]
            val e1x = patchX[b] - patchX[a]; val e1z = patchZ[b] - patchZ[a]
            val e2x = patchX[c] - patchX[a]; val e2z = patchZ[c] - patchZ[a]
            val px = gx - patchX[a]; val pz = gz - patchZ[a]; val det = e1x * e2z - e1z * e2x
            val wb = (px * e2z - pz * e2x) / det; val wc = (e1x * pz - e1z * px) / det
            val inside = minOf(1 - wb - wc, wb, wc)
            if (inside > best) {
                best = inside
                height = patchY[a] + wb * (patchY[b] - patchY[a]) + wc * (patchY[c] - patchY[a])
                if (inside >= 0) break
            }
        }
        return height
    }
    private fun meshActive(i: Int, j: Int): Boolean {
        val k = j * mesh!!.columns + i
        return (meshBits!![k shr 3].toInt() shr (k and 7)) and 1 == 1
    }
    /** Scene height of mesh node (i, j): the tile node, clamped at the tile edges. */
    private fun meshNode(i: Int, j: Int): Double {
        val m = mesh!!; val t = meshTile!!
        val col = (i - kotlin.math.round((t.x - m.x) / m.step).toInt()).coerceIn(0, t.columns - 1)
        val row = (j - kotlin.math.round((t.z - m.z) / m.step).toInt()).coerceIn(0, t.rows - 1)
        return toSceneHeight(height(t, row * t.columns + col) ?: 0.0)
    }
    /** Rendered ground (scene units): the adaptive mesh, identical to the UI's SurfaceMesh.height(). */
    fun surfaceHeight(x: Double, z: Double): Double {
        val m = mesh ?: return sample(x, z)?.sceneY ?: -.02
        val gx = (x - m.x) / m.step; val gz = (z - m.z) / m.step; val r = meshRoot
        val ri = floor(gx / r).toInt().coerceIn(0, (m.columns - 1) / r - 1)
        val rj = floor(gz / r).toInt().coerceIn(0, (m.rows - 1) / r - 1)
        val i0 = ri * r; val j0 = rj * r; val u = gx - i0; val v = gz - j0
        var rx: Int; var rz: Int; var ax: Int; var az: Int; var bx: Int; var bz: Int
        if ((ri + rj) % 2 == 0) {
            if (u >= v) { rx = i0 + r; rz = j0; ax = i0; az = j0; bx = i0 + r; bz = j0 + r }
            else { rx = i0; rz = j0 + r; ax = i0 + r; az = j0 + r; bx = i0; bz = j0 }
        } else if (u + v <= r) { rx = i0; rz = j0; ax = i0 + r; az = j0; bx = i0; bz = j0 + r }
        else { rx = i0 + r; rz = j0 + r; ax = i0; az = j0 + r; bx = i0 + r; bz = j0 }
        while (true) {
            val sx = ax + bx; val sz = az + bz
            if (sx and 1 != 0 || sz and 1 != 0) break
            val mx = sx shr 1; val mz = sz shr 1
            if (!meshActive(mx, mz)) break
            val side = (mx - rx) * (gz - rz) - (mz - rz) * (gx - rx)
            val sideA = (mx - rx) * (az - rz) - (mz - rz) * (ax - rx)
            if ((side >= 0) == (sideA >= 0)) { bx = ax; bz = az; ax = rx; az = rz }
            else { ax = bx; az = bz; bx = rx; bz = rz }
            rx = mx; rz = mz
        }
        patchLeaves[leafKey(rx, rz, ax, az, bx, bz)]?.let { return patchHeight(it, gx, gz) }
        val e1x = (ax - rx).toDouble(); val e1z = (az - rz).toDouble()
        val e2x = (bx - rx).toDouble(); val e2z = (bz - rz).toDouble()
        val px = gx - rx; val pz = gz - rz; val det = e1x * e2z - e1z * e2x
        val wa = (px * e2z - pz * e2x) / det; val wb = (e1x * pz - e1z * px) / det
        val hr = meshNode(rx, rz)
        return hr + wa * (meshNode(ax, az) - hr) + wb * (meshNode(bx, bz) - hr)
    }
    private val groundCache = java.util.concurrent.ConcurrentHashMap<Pair<Double,Double>,Double>()
    fun groundHeight(x: Double, z: Double): Double {
        sample(x,z)?.let { return if(data.revision.startsWith("lm-dtm-"))surfaceHeight(x,z) else it.sceneY }
        val key=x to z
        groundCache[key]?.let { return it }
        for(tile in tiles.filter { it.kind == "DTM" }) {
            val u=(x-tile.x)/tile.step; val v=(z-tile.z)/tile.step
            if(u<0||v<0||u>tile.columns-1||v>tile.rows-1) continue
            for(radius in listOf(2,4,8,16,32)) {
                val candidates=mutableListOf<Pair<Double,Double>>()
                for(r in (floor(v).toInt()-radius).coerceAtLeast(0)..(kotlin.math.ceil(v).toInt()+radius).coerceAtMost(tile.rows-1))
                    for(c in (floor(u).toInt()-radius).coerceAtLeast(0)..(kotlin.math.ceil(u).toInt()+radius).coerceAtMost(tile.columns-1)) {
                        val h=height(tile,r*tile.columns+c) ?: continue
                        candidates.add(((u-c)*(u-c)+(v-r)*(v-r)) to h)
                    }
                if(candidates.isNotEmpty()) {
                    var sum=0.0;var weight=0.0
                    for((d,h) in candidates.sortedBy { it.first }.take(4)) { val w=1.0/d.coerceAtLeast(1e-9);sum+=h*w;weight+=w }
                    val y=toSceneHeight(sum/weight)
                    if(groundCache.size<100000) groundCache[key]=y
                    return y
                }
            }
        }
        return -.02
    }
    /** Legacy flat-scene conversion (ground plane −0.02 onto the terrain); non-DTM preview only. */
    fun relativeHeight(x: Double, z: Double, legacyY: Double) = legacyY + groundHeight(x, z) + .02
    /** Same as terrain.ts placeLegacy(): a legacy model point on the surface it was modelled on. */
    fun placeLegacy(x: Double, z: Double, legacyY: Double, standsOn: Double, base: Double? = null) =
        if(!data.revision.startsWith("lm-dtm-")) relativeHeight(x,z,legacyY) else (base ?: groundHeight(x,z)) + legacyY - standsOn
    fun bridgeHeight(x:Double,z:Double,legacyY:Double):Double? {
        if(!data.revision.startsWith("lm-dtm-"))return null
        val key=listOf(x,z,legacyY).joinToString(","){String.format(java.util.Locale.ROOT,"%.3f",it)}
        return bridgeProfiles.points[key]
    }
    /**
     * Network reference height, identical to terrain.ts roadHeight(): with the DTM the legacy street
     * surface (LEGACY_STREET above LEGACY_GROUND, 2.43 m) is removed from every road/rail height,
     * including the estimated bridge profiles. Legacy rises (elevated structures) are kept.
     */
    fun roadHeight(x:Double,z:Double,legacyY:Double):Double {
        if(!data.revision.startsWith("lm-dtm-")) return bridgeHeight(x,z,legacyY) ?: relativeHeight(x,z,legacyY)
        return (bridgeHeight(x,z,legacyY) ?: relativeHeight(x,z,legacyY.coerceAtLeast(LEGACY_ROAD))) - (LEGACY_STREET - LEGACY_GROUND)
    }
    /**
     * Engineered span height of the segment a->b at u (bridge-profiles.json `spans`, rules v4): an
     * at-grade way bridging an underpass or culvert trench linearly. Same lookup as terrain.ts spans().
     */
    fun spanHeight(ax: Double, az: Double, bx: Double, bz: Double, u: Double): Double? {
        if(!data.revision.startsWith("lm-dtm-")) return null
        val key = { x: Double, z: Double -> String.format(java.util.Locale.ROOT,"%.3f,%.3f",x,z) }
        bridgeProfiles.spans[key(ax,az)+"|"+key(bx,bz)]?.let { return spanAt(it, u) }
        bridgeProfiles.spans[key(bx,bz)+"|"+key(ax,az)]?.let { return spanAt(it, 1-u) }
        return null
    }
    private fun spanAt(pieces: List<List<Double>>, u: Double): Double? {
        for ((t0, t1, y0, y1) in pieces)
            if (u >= t0 - 1e-9 && u <= t1 + 1e-9) return if (t1 - t0 < 1e-12) y0 else y0 + (y1 - y0) * (u - t0) / (t1 - t0)
        return null
    }
    /** Linear (deck) profile only between two profiled ends; identical to terrain.ts linearBetween(). */
    fun linearBetween(a: List<Double>, b: List<Double>) =
        !data.revision.startsWith("lm-dtm-") || (bridgeHeight(a[0],a[2],a[1])!=null && bridgeHeight(b[0],b[2],b[1])!=null)
    /**
     * Network reference height between two network points [x, legacyY, z], identical to terrain.ts
     * profileHeight() (rules v4): surveyed deck, engineered span, linear deck between two profiled ends,
     * otherwise the terrain with the end offsets tapered along the segment.
     */
    private val integratedSegments: Set<String> = mesh?.patches?.integrated?.toSet() ?: emptySet()
    /** Same as terrain.ts integrated(): a segment absorbed into a road breakline patch (Step 5). */
    fun integrated(ax: Double, az: Double, bx: Double, bz: Double): Boolean {
        if (integratedSegments.isEmpty()) return false
        val key = { x: Double, z: Double -> String.format(java.util.Locale.ROOT, "%.3f,%.3f", x, z) }
        return key(ax, az) + "|" + key(bx, bz) in integratedSegments || key(bx, bz) + "|" + key(ax, az) in integratedSegments
    }
    fun profileHeight(a: List<Double>, b: List<Double>, t: Double, absoluteRH2000: Pair<Double, Double>? = null): Double {
        val u = t.coerceIn(0.0, 1.0)
        if (absoluteRH2000 != null) return toSceneHeight(absoluteRH2000.first*(1-u)+absoluteRH2000.second*u)
        if(!data.revision.startsWith("lm-dtm-"))
            return if(bridgeHeight(a[0],a[2],a[1])!=null || bridgeHeight(b[0],b[2],b[1])!=null)
                roadHeight(a[0],a[2],a[1])*(1-u)+roadHeight(b[0],b[2],b[1])*u
            else roadHeight(a[0]*(1-u)+b[0]*u,a[2]*(1-u)+b[2]*u,a[1]*(1-u)+b[1]*u)
        // Absorbed into a breakline patch: draped on the surface it shaped, zero offset (Step 5).
        if (integrated(a[0], a[2], b[0], b[2]))
            return groundHeight(a[0]*(1-u)+b[0]*u, a[2]*(1-u)+b[2]*u) - (LEGACY_STREET - LEGACY_ROAD)
        // A span carries the network reference itself, exactly as drawn (UI cityMesh surfaceAt). Vehicle
        // offsets above it (rail head, ride height) belong to the vehicle contracts (RailContract).
        spanHeight(a[0],a[2],b[0],b[2],u)?.let { return it }
        val ya = roadHeight(a[0],a[2],a[1]); val yb = roadHeight(b[0],b[2],b[1])
        if (linearBetween(a, b)) return ya*(1-u)+yb*u
        return groundHeight(a[0]*(1-u)+b[0]*u,a[2]*(1-u)+b[2]*u) +
            (ya-groundHeight(a[0],a[2]))*(1-u) + (yb-groundHeight(b[0],b[2]))*u
    }
    @Serializable private data class BridgeProfiles(
        val method:String, val points:Map<String,Double>, val spans:Map<String,List<List<Double>>> = emptyMap(),
    )
    companion object {
        const val DATASET_VERSION = 2
        /** Pre-DTM flat-scene levels (scene units); see terrain.ts LEGACY_LEVELS. */
        const val LEGACY_GROUND = -.02
        const val LEGACY_ROAD = .071
        const val LEGACY_STREET = .077
        private val bridgeProfiles by lazy {
            // `spanInfo` documents the spans for people and tests; the server needs `points` and `spans`.
            Json { ignoreUnknownKeys = true }.decodeFromString<BridgeProfiles>(checkNotNull(TerrainService::class.java.getResourceAsStream("/bridge-profiles.json")).bufferedReader().use{it.readText()})
        }
        val bundled by lazy {
            TerrainService(Json.decodeFromString<TerrainDataset>(checkNotNull(TerrainService::class.java.getResourceAsStream("/terrain.json"))
                .bufferedReader().use { it.readText().removePrefix("\uFEFF") }))
        }
    }
}
