package com.norrkoping.simulator

import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** Commands mutate the aggregate under one lock; immutable snapshots are the read projection. */
class CitySimulationService {
    private val mutex=Mutex()
    private var traffic=RoadTraffic()
    private var flight=RegionalFlight()
    private var rail=RailTraffic()
    private var settings=TrafficSettings(buses=12,trams=8,trains=2)
    private var paused=false
    private var time=0.0
    private var sequence=0L
    private var eventId=0L
    private val events=ArrayDeque<Activity>()
    private val state=MutableStateFlow(CitySnapshot(0,0.0,false,settings,emptyList(),emptyList(),0,emptyList()))
    val world=state.asStateFlow()
    init {traffic.configure(settings);rail.configure(settings);log("ready","Norrköping Simulator är redo.");publish()}
    private fun log(kind:String,message:String){events.addFirst(Activity(++eventId,time,kind,message));while(events.size>25)events.removeLast()}
    private fun publish(){state.value=CitySnapshot(++sequence,time,paused,settings,traffic.views()+rail.views(),if(settings.flights)listOf(flight.view())else emptyList(),flight.completed,events.toList())}
    suspend fun execute(command:CityCommand)=mutex.withLock {
        when(command){
            is CityCommand.Configure->{val s=command.settings;require(s.compact in 0..600&&s.estate in 0..600&&s.trucks in 0..150&&s.buses in 0..80&&s.trams in 0..40&&s.trains in 0..12&&s.compact+s.estate+s.trucks+s.buses<=1000){"Välj högst 1 000 vägfordon: 600 av varje biltyp, 150 lastbilar och 80 bussar. Högst 40 spårvagnar och 12 tåg."};require(s.timeScale.isFinite()&&s.timeScale in 1.0..20.0){"Tidsfaktor måste vara 1–20."};traffic.configure(s);rail.configure(s);if(s.flights&&!settings.flights)flight=RegionalFlight();settings=s;log("traffic","Trafiken uppdaterad: ${s.compact+s.estate+s.trucks+s.buses} vägfordon, ${s.trams} spårvagnar och ${s.trains} tåg.")}
            is CityCommand.Pause->{paused=command.value;log("pause",if(paused)"Simuleringen pausad." else "Simuleringen fortsätter.")}
            CityCommand.Reset->{traffic=RoadTraffic();flight=RegionalFlight();rail=RailTraffic();traffic.configure(settings);rail.configure(settings);time=0.0;paused=false;events.clear();log("reset","Simuleringen återställd.")}
        };publish()
    }
    suspend fun step(dt:Double)=mutex.withLock {
        if(!paused){var remaining=dt*settings.timeScale;while(remaining>.000001){val tick=minOf(.05,remaining);time+=tick;traffic.tick(tick,time);rail.tick(tick);if(settings.flights&&flight.tick(tick))log("landing","${flight.view().destination}: NK-01 har landat.");remaining-=tick}}
        publish()
    }
    suspend fun run(){while(currentCoroutineContext().isActive){val start=System.nanoTime();step(.1);delay((100-(System.nanoTime()-start)/1_000_000).coerceAtLeast(1))}}
}
