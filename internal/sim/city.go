package sim

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

type CityCommand interface{ isCityCommand() }

type (
	ConfigureCity struct{ Settings TrafficSettings }
	PauseCity     struct{ Paused bool }
	ResetCity     struct{}
)

func (ConfigureCity) isCityCommand() {}
func (PauseCity) isCityCommand()     {}
func (ResetCity) isCityCommand()     {}

// CityService is the city simulation behind /api/simulator/*. Commands mutate the aggregate under one
// lock; immutable snapshots are the read projection.
type CityService struct {
	mu       sync.Mutex
	traffic  *RoadTraffic
	flight   *RegionalFlight
	rail     *RailTraffic
	settings TrafficSettings
	paused   bool
	time     float64
	sequence int64
	eventID  int64
	events   []Activity
	revision string
	World    *State[CitySnapshot]
}

// NewCityService starts with the default traffic on the bundled map.
func NewCityService() *CityService {
	s := &CityService{traffic: NewBundledRoadTraffic(), flight: NewBundledRegionalFlight(), rail: NewBundledRailTraffic(),
		settings: DefaultTrafficSettings(), revision: BundledTerrain().Data.Revision, World: newState[CitySnapshot](nil)}
	s.settings.Buses, s.settings.Trams, s.settings.Trains = 12, 8, 2
	if err := s.configure(s.settings); err != nil {
		panic(err)
	}
	s.log("ready", "Norrköping Simulator är redo.")
	s.publish()
	return s
}

func (s *CityService) configure(settings TrafficSettings) error {
	if err := s.traffic.Configure(settings); err != nil {
		return err
	}
	return s.rail.Configure(settings)
}

// log puts the newest event first and keeps the last 25.
func (s *CityService) log(kind, message string) {
	s.eventID++
	events := make([]Activity, 0, len(s.events)+1)
	events = append(events, Activity{s.eventID, s.time, kind, message})
	events = append(events, s.events...)
	if len(events) > 25 {
		events = events[:25]
	}
	s.events = events
}

func (s *CityService) publish() {
	s.sequence++
	aircraft := []AircraftView{}
	if s.settings.Flights {
		aircraft = append(aircraft, s.flight.View())
	}
	s.World.set(&CitySnapshot{s.sequence, s.time, s.paused, s.settings, append(s.traffic.Views(), s.rail.Views()...), aircraft,
		s.flight.Completed, s.events, s.revision})
}

// Execute applies a command. A rejected command changes nothing and publishes nothing.
func (s *CityService) Execute(command CityCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch c := command.(type) {
	case ConfigureCity:
		v := c.Settings
		if v.Compact < 0 || v.Compact > 600 || v.Estate < 0 || v.Estate > 600 || v.Trucks < 0 || v.Trucks > 150 ||
			v.Buses < 0 || v.Buses > 80 || v.Trams < 0 || v.Trams > 40 || v.Trains < 0 || v.Trains > 12 ||
			v.Compact+v.Estate+v.Trucks+v.Buses > 1000 {
			return invalid("Välj högst 1 000 vägfordon: 600 av varje biltyp, 150 lastbilar och 80 bussar. Högst 40 spårvagnar och 12 tåg.")
		}
		if !isFinite(v.TimeScale) || v.TimeScale < 1.0 || v.TimeScale > 20.0 {
			return invalid("Tidsfaktor måste vara 1–20.")
		}
		if err := s.configure(v); err != nil {
			return err
		}
		if v.Flights && !s.settings.Flights {
			s.flight = NewBundledRegionalFlight()
		}
		s.settings = v
		s.log("traffic", fmt.Sprintf("Trafiken uppdaterad: %d vägfordon, %d spårvagnar och %d tåg.", v.Compact+v.Estate+v.Trucks+v.Buses, v.Trams, v.Trains))
	case PauseCity:
		s.paused = c.Paused
		if s.paused {
			s.log("pause", "Simuleringen pausad.")
		} else {
			s.log("pause", "Simuleringen fortsätter.")
		}
	case ResetCity:
		s.traffic, s.flight, s.rail = NewBundledRoadTraffic(), NewBundledRegionalFlight(), NewBundledRailTraffic()
		if err := s.configure(s.settings); err != nil {
			return err
		}
		s.time, s.paused, s.events = 0.0, false, nil
		s.log("reset", "Simuleringen återställd.")
	default:
		return fmt.Errorf("unknown command %T", command)
	}
	s.publish()
	return nil
}

// Step advances dt seconds of wall time, scaled by the time factor, in sub-steps of at most 0.05 s.
func (s *CityService) Step(dt float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paused {
		for remaining := dt * s.settings.TimeScale; remaining > .000001; {
			tick := math.Min(.05, remaining)
			s.time += tick
			s.traffic.Tick(tick, s.time)
			s.rail.Tick(tick)
			if s.settings.Flights && s.flight.Tick(tick) {
				s.log("landing", s.flight.View().Destination+": NK-01 har landat.")
			}
			remaining -= tick
		}
	}
	s.publish()
}

// Run steps at 10 Hz until the context is cancelled.
func (s *CityService) Run(ctx context.Context) {
	for ctx.Err() == nil {
		started := time.Now()
		s.Step(.1)
		sleepRest(ctx, 100*time.Millisecond, started)
	}
}
