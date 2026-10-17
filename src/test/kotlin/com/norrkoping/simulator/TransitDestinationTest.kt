package com.norrkoping.simulator
import kotlin.test.*
class TransitDestinationTest {
 @Test fun destinationChangesAndResetsEachLap() {
  val route=TransitRoute("tram-2","tram","Spårvagn 2",true,
   listOf(listOf(0.0,.089,0.0),listOf(10.0,.089,0.0),listOf(10.0,.089,10.0),listOf(0.0,.089,10.0),listOf(0.0,.089,0.0)),
   emptyList(),listOf(TransitStop(0.0,"Kvarnberget"),TransitStop(20.0,"Fridvalla")))
  val rail=RailTraffic(listOf(route));rail.configure(TrafficSettings(trams=1))
  assertEquals("Kvarnberget",rail.views().single().destination)
  val changes=mutableListOf<String>();var last=""
  repeat(2500) {
   rail.tick(.1);val view=rail.views().single();assertEquals("Spårvagn 2",view.road)
   if(view.destination!=last) {last=requireNotNull(view.destination);changes.add(last)}
  }
  assertTrue(changes.size>=4)
  assertEquals(listOf("Kvarnberget","Fridvalla","Kvarnberget","Fridvalla"),changes.take(4))
 }
 @Test fun bundledRoutesCarryBothDestinations() {
  val routes=TransportNetwork.bundled.transitRoutes.associateBy { it.id }
  assertEquals(listOf("Kvarnberget","Fridvalla"),routes.getValue("tram-2").destinations.map { it.name })
  assertEquals(listOf("Vidablick","Klockaretorpet"),routes.getValue("tram-3").destinations.map { it.name })
 }
}
