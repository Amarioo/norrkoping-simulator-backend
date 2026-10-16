package com.norrkoping.simulator

import kotlin.test.*
import kotlin.math.*

class TransitRegressionTest {
    @Test fun `underpasses preserve through traffic instead of dead ends`() {
        val n=TransportNetwork.bundled
        for(way in listOf(39445117L,89615815L)) {
            val edges=n.edges.filter { it.way==way }
            assertTrue(edges.isNotEmpty(),"Ståthögavägen underpass $way is missing")
            assertTrue(edges.all { n.outgoing[it.to].orEmpty().isNotEmpty() })
        }
        assertTrue(n.nodes.filter { it.signal }.all { it.signalHorizontal!=null })
    }
    @Test fun `a queued approach restarts on green including short incoming edges`() {
        val n=TransportNetwork(
            listOf(RoadNode(1,0.0,0.0),RoadNode(2,4.0,0.0),RoadNode(3,4.1,0.0,signal=true,signalHorizontal=true),RoadNode(4,10.0,0.0),RoadNode(5,10.0,5.0),RoadNode(6,0.0,5.0)),
            listOf(RoadEdge(0,1,4.0,.5,.3,true,"Queue",1,true),RoadEdge(1,2,.1,.5,.3,true,"Short approach",2,true),RoadEdge(2,3,5.9,.5,.3,true,"After signal",3,true),RoadEdge(3,4,5.0,.5,.3,true,"Loop",4,true),RoadEdge(4,5,10.0,.5,.3,true,"Loop",5,true),RoadEdge(5,0,5.0,.5,.3,true,"Loop",6,true)),emptyList(),emptyList())
        val traffic=RoadTraffic(n);traffic.configure(TrafficSettings(7,0,2,false,1.0))
        repeat(6000) { traffic.tick(.05,30.0) }
        val stopped=traffic.views().associateBy { it.id }
        assertTrue(stopped.values.count { it.stopped }>=7)
        repeat(2000) { traffic.tick(.05,5.0) }
        assertTrue(traffic.views().count { v -> hypot(v.pos[0]-stopped.getValue(v.id).pos[0],v.pos[2]-stopped.getValue(v.id).pos[2])>1 }>=7)
    }
    @Test fun `regional traffic keeps flowing after ten minutes`() {
        val traffic=RoadTraffic();traffic.configure(TrafficSettings(100,60,30,false,1.0,buses=12))
        repeat(6000) { traffic.tick(.05,it*.05) }
        val before=traffic.views().associateBy { it.id }
        repeat(6000) { traffic.tick(.05,300+it*.05) }
        val stuck=traffic.views().filter { v -> hypot(v.pos[0]-before.getValue(v.id).pos[0],v.pos[2]-before.getValue(v.id).pos[2])<.1 }
        assertTrue(stuck.size<20,"Still blocked for five minutes: ${stuck.map { it.id to it.road }}")
    }
    @Test fun `bus stops again on the next lap`() {
        val nodes=listOf(RoadNode(1,0.0,0.0),RoadNode(2,3.0,0.0,busStop="Testhållplats"),RoadNode(3,3.0,3.0),RoadNode(4,0.0,3.0))
        val edges=(0..3).map { RoadEdge(it,(it+1)%4,3.0,.5,.3,true,"Bus loop",it.toLong(),true) }
        val traffic=RoadTraffic(TransportNetwork(nodes,edges,emptyList(),emptyList()))
        traffic.configure(TrafficSettings(0,0,0,false,1.0,buses=1))
        var stops=0;var wasStopped=false
        repeat(8000) {
            traffic.tick(.05,it*.05)
            val stopped=traffic.views().single().stop!=null
            if(stopped&&!wasStopped)stops++
            wasStopped=stopped
        }
        assertTrue(stops>=3,"Bus stopped only $stops times")
    }
    @Test fun `articulated rail traffic moves dwells and resumes without jumps`() {
        val rail=RailTraffic();rail.configure(TrafficSettings(trams=4,trains=2))
        val previous=rail.views().associate { it.id to it.pos }.toMutableMap()
        val dwelled=mutableSetOf<String>();val resumed=mutableSetOf<String>()
        repeat(18000) {
            rail.tick(.1)
            for(v in rail.views()) {
                assertEquals(if(v.kind=="train")4 else 5,v.sections.size)
                assertTrue(v.sections.all { it.pos.all(Double::isFinite) })
                assertTrue(hypot(v.pos[0]-previous.getValue(v.id)[0],v.pos[2]-previous.getValue(v.id)[2])<.16,"Jump ${v.kind}")
                if(v.stop!=null)dwelled.add(v.kind)
                if(v.kind in dwelled && v.speed>5)resumed.add(v.kind)
                previous[v.id]=v.pos
            }
        }
        assertEquals(setOf("tram","train"),resumed)
        rail.configure(TrafficSettings());assertTrue(rail.views().isEmpty())
    }
}
