package sim

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// The legacy drone simulation behind /api/world, /api/sse/* and /api/commands/*. The current UI uses the
// city simulation (/api/simulator/*); these endpoints remain for compatibility.

type Command interface{ isCommand() }

type (
	DispatchCommand   struct{ Request DispatchRequest }
	AddZoneCommand    struct{ Request ZoneRequest }
	RemoveZoneCommand struct{ ID string }
	PauseCommand      struct{ Paused bool }
	WindCommand       struct{ Request WindRequest }
	RecallCommand     struct{}
	ResetCommand      struct{}
)

func (DispatchCommand) isCommand()   {}
func (AddZoneCommand) isCommand()    {}
func (RemoveZoneCommand) isCommand() {}
func (PauseCommand) isCommand()      {}
func (WindCommand) isCommand()       {}
func (RecallCommand) isCommand()     {}
func (ResetCommand) isCommand()      {}

type eventKind int

const (
	evDroneChanged eventKind = iota
	evZoneAdded
	evZoneRemoved
	evPaused
	evWindChanged
	evTravelled
	evDelivered
	evReset
	evLogged
)

// domainEvent is what the write side tells the read projection.
type domainEvent struct {
	kind    eventKind
	drone   DroneView
	zone    ZoneView
	id      string
	paused  bool
	wind    []float64
	metres  float64
	logKind string
	message string
}

func logged(kind, message string) domainEvent {
	return domainEvent{kind: evLogged, logKind: kind, message: message}
}

type drone struct {
	id       string
	pos      Vec3
	target   Vec3
	home     Vec3
	weight   float64
	battery  float64
	state    string
	mission  string
	recycle  bool
	idleTime float64
	place    string
	route    []Vec3
}

func (d *drone) view() DroneView {
	route := make([][]float64, len(d.route))
	for i, p := range d.route {
		route[i] = p.Array()
	}
	return DroneView{d.id, d.pos.Array(), d.target.Array(), d.battery, d.state, d.weight, route, d.place}
}

// droneWorld is the mutable write aggregate. Only DroneService may access it, under its mutex.
type droneWorld struct {
	random    *KRandom
	drones    []*drone
	zones     []ZoneView
	nextDrone int
	nextZone  int
	paused    bool
	time      float64
	wind      Vec3
}

func newDroneWorld() *droneWorld {
	return &droneWorld{random: NewKRandom(42), nextDrone: 1, nextZone: 1}
}

func (w *droneWorld) plan(d *drone) {
	d.route = PathRoute(d.pos, d.target, w.zones)
	if len(d.route) == 0 {
		d.state = "HOLDING"
	} else {
		d.state = d.mission
	}
}

// drawPlace draws a delivery address from the Norrköping city map; a named request always wins.
func (w *droneWorld) drawPlace(requested *string) *Place {
	if place := FindPlace(requested); place != nil {
		return place
	}
	return &Places[w.random.NextIntN(len(Places))]
}

func (w *droneWorld) destination(altitude float64, place *Place) Vec3 {
	x := place.X + w.random.NextDoubleRange(-1.2, 1.2)
	z := place.Z + w.random.NextDoubleRange(-1.2, 1.2)
	return Vec3{x, altitude, z}
}

func (w *droneWorld) replanActive(events []domainEvent) []domainEvent {
	for _, d := range w.drones {
		if d.state != "IDLE" {
			w.plan(d)
			events = append(events, domainEvent{kind: evDroneChanged, drone: d.view()})
		}
	}
	return events
}

func (w *droneWorld) handle(command Command) ([]domainEvent, error) {
	var events []domainEvent
	switch c := command.(type) {
	case DispatchCommand:
		r := c.Request
		if r.Count < 1 || r.Count > 20 {
			return nil, invalid("Count must be between 1 and 20.")
		}
		if len(w.drones)+r.Count > 100 {
			return nil, invalid("Fleet limit is 100 drones. Reset to clear the fleet.")
		}
		if !isFinite(r.WeightKg) || r.WeightKg < 0.1 || r.WeightKg > 5.0 {
			return nil, invalid("Payload must be between 0.1 and 5 kg.")
		}
		var hub *HubView
		for i := range Hubs {
			if Hubs[i].ID == r.OriginHub {
				hub = &Hubs[i]
				break
			}
		}
		if hub == nil {
			return nil, invalid("Unknown origin hub.")
		}
		if r.Destination != nil && FindPlace(r.Destination) == nil {
			return nil, invalid(fmt.Sprintf("Unknown destination \"%s\". Choose one of the places on the city map.", *r.Destination))
		}
		for i := 0; i < r.Count; i++ {
			altitude := 18.0 + float64((w.nextDrone%3)*4)
			home := Vec3{hub.Pos[0], altitude, hub.Pos[2]}
			place := w.drawPlace(r.Destination)
			d := &drone{id: fmt.Sprintf("D-%03d", w.nextDrone), pos: home, target: w.destination(altitude, place), home: home,
				weight: r.WeightKg, battery: 100.0, state: "DELIVERING", mission: "DELIVERING", recycle: true, place: place.Name}
			w.nextDrone++
			w.plan(d)
			w.drones = append(w.drones, d)
			events = append(events, domainEvent{kind: evDroneChanged, drone: d.view()})
		}
		to := ""
		if place := FindPlace(r.Destination); place != nil {
			to = " to " + place.Name
		}
		events = append(events, logged("dispatch", fmt.Sprintf("Dispatched %d drone(s) from %s%s.", r.Count, hub.Label, to)))
	case AddZoneCommand:
		r := c.Request
		if len(w.zones) >= 24 {
			return nil, invalid("Zone limit is 24.")
		}
		if len(r.Center) != 3 || !isFinite(r.Center[0]) || !isFinite(r.Center[1]) || !isFinite(r.Center[2]) {
			return nil, invalid("Center must contain exactly three finite coordinates.")
		}
		if math.Abs(r.Center[0]) > 65 || math.Abs(r.Center[2]) > 65 || r.Center[1] < 0.0 || r.Center[1] > 35.0 {
			return nil, invalid("Zone center is outside the flight area.")
		}
		if !isFinite(r.Radius) || r.Radius < 4.0 || r.Radius > 18.0 {
			return nil, invalid("Radius must be between 4 and 18 metres.")
		}
		zone := ZoneView{fmt.Sprintf("Z-%d", w.nextZone), append([]float64{}, r.Center...), r.Radius}
		center := vecOf(r.Center)
		for _, hub := range Hubs {
			if math.Hypot(hub.Pos[0]-center.X, hub.Pos[2]-center.Z) < r.Radius+6 {
				return nil, invalid("Keep zones clear of the hubs.")
			}
		}
		for _, d := range w.drones {
			if !PathClear(d.pos, d.pos, []ZoneView{zone}) {
				return nil, invalid("A drone is inside this zone. Choose a clear area or try again.")
			}
		}
		w.zones = append(w.zones, zone)
		w.nextZone++
		events = append(events, domainEvent{kind: evZoneAdded, zone: zone})
		events = w.replanActive(events)
		events = append(events, logged("zone", zone.ID+" activated. Routes recalculated."))
	case RemoveZoneCommand:
		index := -1
		for i, zone := range w.zones {
			if zone.ID == c.ID {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, invalid("Unknown zone.")
		}
		w.zones = append(w.zones[:index:index], w.zones[index+1:]...)
		events = append(events, domainEvent{kind: evZoneRemoved, id: c.ID})
		events = w.replanActive(events)
		events = append(events, logged("zone", c.ID+" removed."))
	case PauseCommand:
		w.paused = c.Paused
		events = append(events, domainEvent{kind: evPaused, paused: w.paused})
		if w.paused {
			events = append(events, logged("system", "Simulation paused."))
		} else {
			events = append(events, logged("system", "Simulation resumed."))
		}
	case WindCommand:
		r := c.Request
		if !isFinite(r.X) || !isFinite(r.Z) || math.Abs(r.X) > 10 || math.Abs(r.Z) > 10 {
			return nil, invalid("Wind components must be between -10 and 10 m/s.")
		}
		w.wind = Vec3{r.X, 0.0, r.Z}
		events = append(events, domainEvent{kind: evWindChanged, wind: []float64{r.X, r.Z}})
		events = append(events, logged("weather", "Wind set to "+javaDouble(r.X)+", "+javaDouble(r.Z)+" m/s."))
	case RecallCommand:
		for _, d := range w.drones {
			d.recycle = false
		}
		for _, d := range w.drones {
			if d.state != "IDLE" {
				d.target = d.home
				d.mission = "RETURNING"
				w.plan(d)
				events = append(events, domainEvent{kind: evDroneChanged, drone: d.view()})
			}
		}
		events = append(events, logged("recall", "All active drones recalled to their hubs."))
	case ResetCommand:
		w.drones, w.zones, w.paused, w.time, w.wind = nil, nil, false, 0.0, Vec3{}
		events = append(events, domainEvent{kind: evReset})
		request := NewDispatchRequest()
		request.Count = 8
		dispatched, err := w.handle(DispatchCommand{request})
		if err != nil {
			return nil, err
		}
		events = append(events, dispatched...)
	default:
		return nil, fmt.Errorf("unknown command %T", command)
	}
	return events, nil
}

func (w *droneWorld) tick(dt float64) []domainEvent {
	if !isFinite(dt) || dt <= 0 || dt > 1 {
		panic("drone tick: dt must be in (0, 1]")
	}
	if w.paused {
		return nil
	}
	w.time += dt
	var events []domainEvent
	changed := func(d *drone) { events = append(events, domainEvent{kind: evDroneChanged, drone: d.view()}) }
	for _, d := range w.drones {
		if d.state == "IDLE" {
			d.idleTime += dt
			d.battery = math.Min(d.battery+dt*12, 100.0)
			if d.recycle && d.idleTime >= 4 && d.battery >= 99 {
				place := w.drawPlace(nil)
				d.idleTime = 0.0
				d.place = place.Name
				d.target = w.destination(d.home.Y, place)
				d.mission = "DELIVERING"
				w.plan(d)
				events = append(events, logged("dispatch", d.id+" recharged and dispatched again to "+place.Name+"."))
			}
			changed(d)
			continue
		}
		if d.state == "EMERGENCY" {
			continue
		}
		if d.battery < 22 && d.mission == "DELIVERING" {
			d.target = d.home
			d.mission = "RETURNING"
			w.plan(d)
			events = append(events, logged("battery", d.id+" returning on low battery."))
		}
		d.battery = math.Max(d.battery-dt*(0.25+d.weight*0.04), 0.0)
		if d.battery == 0.0 {
			d.state = "EMERGENCY"
			d.route = nil
			events = append(events, logged("alert", d.id+" depleted its battery. Emergency hover marker."))
		} else if len(d.route) > 0 {
			delta := d.route[0].Sub(d.pos)
			direction := delta.Normalized()
			speed := clamp(16-d.weight+w.wind.Dot(direction), 4.0, 25.0)
			step := math.Min(speed*dt, delta.Length())
			next := d.pos.Add(direction.Mul(step))
			if PathClear(d.pos, next, w.zones) {
				d.pos = next
				events = append(events, domainEvent{kind: evTravelled, metres: step})
				if delta.Length() <= step+1e-6 {
					d.route = d.route[1:]
				}
				if len(d.route) == 0 {
					if d.mission == "DELIVERING" {
						events = append(events, domainEvent{kind: evDelivered})
						events = append(events, logged("delivery", d.id+" delivered its payload at "+d.place+"."))
						d.target = d.home
						d.mission = "RETURNING"
						w.plan(d)
					} else {
						d.state = "IDLE"
						d.idleTime = 0.0
						events = append(events, logged("arrival", d.id+" landed at its hub."))
					}
				}
			} else {
				d.state = "HOLDING"
				d.route = nil
			}
		}
		changed(d)
	}
	return events
}

// droneProjection is the event-driven read model; it owns no references to mutable write-side objects.
type droneProjection struct {
	order      []string
	drones     map[string]DroneView
	zones      []ZoneView
	activity   []Activity
	deliveries int
	distance   float64
	paused     bool
	wind       []float64
	eventID    int64
}

func newDroneProjection() *droneProjection {
	return &droneProjection{drones: map[string]DroneView{}, wind: []float64{0.0, 0.0}}
}

func (p *droneProjection) apply(events []domainEvent, time float64) {
	for _, e := range events {
		switch e.kind {
		case evDroneChanged:
			if _, known := p.drones[e.drone.ID]; !known {
				p.order = append(p.order, e.drone.ID)
			}
			p.drones[e.drone.ID] = e.drone
		case evZoneAdded:
			p.zones = append(p.zones[:len(p.zones):len(p.zones)], e.zone)
		case evZoneRemoved:
			kept := make([]ZoneView, 0, len(p.zones))
			for _, zone := range p.zones {
				if zone.ID != e.id {
					kept = append(kept, zone)
				}
			}
			p.zones = kept
		case evPaused:
			p.paused = e.paused
		case evWindChanged:
			p.wind = e.wind
		case evTravelled:
			p.distance += e.metres
		case evDelivered:
			p.deliveries++
		case evReset:
			p.order, p.drones, p.zones, p.activity = nil, map[string]DroneView{}, nil, nil
			p.deliveries, p.distance, p.paused, p.wind = 0, 0.0, false, []float64{0.0, 0.0}
		case evLogged:
			p.eventID++
			activity := make([]Activity, 0, len(p.activity)+1)
			activity = append(activity, Activity{p.eventID, time, e.logKind, e.message})
			activity = append(activity, p.activity...)
			if len(activity) > 50 {
				activity = activity[:50]
			}
			p.activity = activity
		}
	}
}

func (p *droneProjection) views() []DroneView {
	views := make([]DroneView, len(p.order))
	for i, id := range p.order {
		views[i] = p.drones[id]
	}
	return views
}

func (p *droneProjection) world(sequence int64, time float64) *WorldSnapshot {
	zones := append([]ZoneView{}, p.zones...)
	return &WorldSnapshot{sequence, time, p.paused, p.wind, p.views(), zones, Hubs}
}

func (p *droneProjection) telemetry(sequence int64) *TelemetrySnapshot {
	t := &TelemetrySnapshot{Sequence: sequence, Deliveries: p.deliveries, DistanceMetres: p.distance, Events: append([]Activity{}, p.activity...)}
	sum := 0.0
	for _, id := range p.order {
		d := p.drones[id]
		sum += d.Battery
		switch d.State {
		case "DELIVERING":
			t.Delivering++
		case "RETURNING":
			t.Returning++
		case "HOLDING", "EMERGENCY":
			t.Holding++
		}
		if d.State != "IDLE" {
			t.Active++
		}
	}
	if len(p.order) > 0 {
		t.AverageBattery = sum / float64(len(p.order))
	}
	return t
}

// DroneService serializes commands and ticks on one lock and publishes immutable snapshots.
type DroneService struct {
	mu         sync.Mutex
	aggregate  *droneWorld
	projection *droneProjection
	sequence   int64
	World      *State[WorldSnapshot]
	Telemetry  *State[TelemetrySnapshot]
}

func NewDroneService() *DroneService {
	s := &DroneService{aggregate: newDroneWorld(), projection: newDroneProjection(),
		World: newState(&WorldSnapshot{}), Telemetry: newState(&TelemetrySnapshot{})}
	request := NewDispatchRequest()
	request.Count = 8
	events, err := s.aggregate.handle(DispatchCommand{request})
	if err != nil {
		panic(err)
	}
	s.projection.apply(events, 0.0)
	s.publish(true)
	return s
}

func (s *DroneService) publish(includeTelemetry bool) {
	s.sequence++
	s.World.set(s.projection.world(s.sequence, s.aggregate.time))
	if includeTelemetry {
		s.Telemetry.set(s.projection.telemetry(s.sequence))
	}
}

// Execute applies a command. A rejected command changes nothing and publishes nothing.
func (s *DroneService) Execute(command Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	events, err := s.aggregate.handle(command)
	if err != nil {
		return err
	}
	s.projection.apply(events, s.aggregate.time)
	s.publish(true)
	return nil
}

// Step advances the simulation; the world is published every second frame, telemetry every tenth.
func (s *DroneService) Step(dt float64, frame int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projection.apply(s.aggregate.tick(dt), s.aggregate.time)
	if frame%2 == 0 {
		s.publish(frame%10 == 0)
	}
}

// Run steps at 20 Hz until the context is cancelled.
func (s *DroneService) Run(ctx context.Context) {
	for frame := int64(1); ctx.Err() == nil; frame++ {
		started := time.Now()
		s.Step(0.05, frame)
		sleepRest(ctx, 50*time.Millisecond, started)
	}
}

// sleepRest waits for what is left of a period that began at started, at least one millisecond.
func sleepRest(ctx context.Context, period time.Duration, started time.Time) {
	rest := period - time.Duration(time.Since(started).Milliseconds())*time.Millisecond
	if rest < time.Millisecond {
		rest = time.Millisecond
	}
	timer := time.NewTimer(rest)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
