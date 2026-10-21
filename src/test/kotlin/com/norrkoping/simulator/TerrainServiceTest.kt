package com.norrkoping.simulator

import kotlin.test.*
import org.junit.jupiter.api.Test

class TerrainServiceTest {
    private val reference = GeoReference("EPSG:3006",569030.0,6494989.0,0.9996584126,-1.0134914515,25.0,"RH2000",0.0)
    private val tile = TerrainTile("test-only","synthetic fixture","https://example.invalid","test","2026-01-01","DTM",1.0,.1,"RH2000","test fixture",0.0,0.0,1.0,2,2,listOf(10.0,20.0,30.0,40.0))
    private fun service(vararg tiles: TerrainTile) = TerrainService(TerrainDataset(TerrainService.DATASET_VERSION,"test",reference,tiles.toList()))
    @Test fun interpolationAndInfrastructureUseOneScale() {
        val s = service(tile)
        assertEquals(25.0,s.sample(.5,.5)?.metresRH2000)
        assertEquals(1.0,s.sample(.5,.5)?.sceneY)
        assertEquals(40.0,s.sample(1.0,1.0)?.metresRH2000)
        assertNull(s.sample(-.01,0.0))
        assertEquals(1.091,s.profileHeight(listOf(0.0,.071,0.0),listOf(1.0,.071,1.0),.5),1e-12)
        assertEquals(5.0,s.profileHeight(listOf(0.0,0.0,0.0),listOf(1.0,0.0,1.0),.5,100.0 to 150.0))
    }
    @Test fun noDataAndDtmPriority() {
        val s = service(tile.copy(id="fallback",kind="DSM-fallback",heights=List(4){50.0}),tile.copy(heights=listOf(10.0,null,30.0,40.0)))
        assertEquals("test-only",s.sample(0.0,0.0)?.sourceId)
        assertEquals("fallback",s.sample(.5,.5)?.sourceId)
        assertNull(service().sample(0.0,0.0))
        assertEquals(-.02,service().groundHeight(0.0,0.0))
    }
    @Test fun rejectsOtherDatumAndBrokenGrid() {
        assertFailsWith<IllegalArgumentException>{service(tile.copy(verticalDatum="EGM2008"))}
        assertFailsWith<IllegalArgumentException>{service(tile.copy(heights=listOf(Double.NaN)))}
    }
    @Test fun binaryNoDataAndWaterAreSeparate() {
        val bytes = java.nio.ByteBuffer.allocate(16).order(java.nio.ByteOrder.LITTLE_ENDIAN)
        listOf(10f,Float.NaN,30f,40f).forEach { bytes.putFloat(it) }
        val encoded = java.util.Base64.getEncoder().encodeToString(bytes.array())
        val s = service(tile.copy(heights=emptyList(),heightsBase64=encoded),tile.copy(id="water",kind="water",heights=List(4){90.0}))
        assertEquals(10.0,s.sample(0.0,0.0)?.metresRH2000)
        assertNull(s.sample(.5,.5))
        assertEquals(90.0,s.sample(.5,.5,true)?.metresRH2000)
    }}
