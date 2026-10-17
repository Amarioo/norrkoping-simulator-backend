package com.norrkoping.simulator

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlin.math.floor
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.util.Base64

@Serializable data class GeoReference(
    val originLatitude: Double, val originLongitude: Double,
    val metresPerDegreeLongitude: Double, val metresPerDegreeLatitude: Double,
    val metresPerUnit: Double, val verticalDatum: String, val verticalOriginMetres: Double,
)
@Serializable data class TerrainTile(
    val id: String, val source: String, val sourceUrl: String, val licence: String,
    val acquired: String, val kind: String, val resolutionMetres: Double,
    val verticalAccuracyMetres: Double?, val verticalDatum: String, val processing: String,
    val x: Double, val z: Double, val step: Double, val columns: Int, val rows: Int,
    val heights: List<Double?>, val heightsBase64: String? = null,
)
@Serializable data class TerrainDataset(val version: Int, val revision: String, val reference: GeoReference, val tiles: List<TerrainTile>)
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
        require(data.version == 1 && data.revision.isNotBlank() && r.verticalDatum == "RH2000")
        require(listOf(r.originLatitude, r.originLongitude, r.verticalOriginMetres).all { it.isFinite() })
        require(listOf(r.metresPerUnit, r.metresPerDegreeLatitude, r.metresPerDegreeLongitude).all { it.isFinite() && it > 0 })
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
    fun project(lat: Double, lon: Double): Pair<Double, Double> = with(data.reference) {
        (lon - originLongitude) * metresPerDegreeLongitude / metresPerUnit to
            (originLatitude - lat) * metresPerDegreeLatitude / metresPerUnit
    }
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
    private val surfaceNodes = java.util.concurrent.ConcurrentHashMap<Pair<Double,Double>,Double>()
    private fun surfaceNode(x:Double,z:Double)=surfaceNodes.computeIfAbsent(x to z){
        val coarse=40.0/data.reference.metresPerUnit
        if(kotlin.math.abs(kotlin.math.abs(x)-64)<1e-8 && kotlin.math.abs(z)<=64) {
            val z0=floor(z/coarse)*coarse;val t=(z-z0)/coarse
            (sample(x,z0)?.sceneY?:-.02)*(1-t)+(sample(x,z0+coarse)?.sceneY?:-.02)*t
        } else if(kotlin.math.abs(kotlin.math.abs(z)-64)<1e-8 && kotlin.math.abs(x)<=64) {
            val x0=floor(x/coarse)*coarse;val t=(x-x0)/coarse
            (sample(x0,z)?.sceneY?:-.02)*(1-t)+(sample(x0+coarse,z)?.sceneY?:-.02)*t
        } else sample(x,z)?.sceneY ?: -.02
    }
    fun surfaceHeight(x:Double,z:Double):Double {
        val step=(if(kotlin.math.abs(x)<64 && kotlin.math.abs(z)<64)10.0 else 40.0)/data.reference.metresPerUnit
        val x0=floor(x/step)*step; val z0=floor(z/step)*step
        val u=(x-x0)/step; val v=(z-z0)/step
        val b=surfaceNode(x0+step,z0); val c=surfaceNode(x0,z0+step)
        return if(u+v<=1)surfaceNode(x0,z0)*(1-u-v)+b*u+c*v
        else surfaceNode(x0+step,z0+step)*(u+v-1)+b*(1-v)+c*(1-u)
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
    fun relativeHeight(x: Double, z: Double, legacyY: Double) = legacyY + groundHeight(x, z) + .02
    fun bridgeHeight(x:Double,z:Double,legacyY:Double):Double? {
        if(!data.revision.startsWith("lm-dtm-"))return null
        val key=listOf(x,z,legacyY).joinToString(","){String.format(java.util.Locale.ROOT,"%.3f",it)}
        return bridgeProfiles.points[key]
    }
    fun roadHeight(x:Double,z:Double,legacyY:Double)=bridgeHeight(x,z,legacyY) ?:
        relativeHeight(x,z,if(data.revision.startsWith("lm-dtm-"))legacyY.coerceAtLeast(.071) else legacyY)
    fun profileHeight(a: List<Double>, b: List<Double>, t: Double, absoluteRH2000: Pair<Double, Double>? = null): Double {
        val u = t.coerceIn(0.0, 1.0)
        if (absoluteRH2000 != null) return toSceneHeight(absoluteRH2000.first*(1-u)+absoluteRH2000.second*u)
        if(!data.revision.startsWith("lm-dtm-") || bridgeHeight(a[0],a[2],a[1])!=null || bridgeHeight(b[0],b[2],b[1])!=null)
            return roadHeight(a[0],a[2],a[1])*(1-u)+roadHeight(b[0],b[2],b[1])*u
        return roadHeight(a[0]*(1-u)+b[0]*u,a[2]*(1-u)+b[2]*u,a[1]*(1-u)+b[1]*u)
    }
    @Serializable private data class BridgeProfiles(val method:String,val points:Map<String,Double>)
    companion object {
        private val bridgeProfiles by lazy {
            Json.decodeFromString<BridgeProfiles>(checkNotNull(TerrainService::class.java.getResourceAsStream("/bridge-profiles.json")).bufferedReader().use{it.readText()})
        }
        val bundled by lazy {
            TerrainService(Json.decodeFromString<TerrainDataset>(checkNotNull(TerrainService::class.java.getResourceAsStream("/terrain.json"))
                .bufferedReader().use { it.readText().removePrefix("\uFEFF") }))
        }
    }
}
