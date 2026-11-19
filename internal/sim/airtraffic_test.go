package sim

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

// airWatch checks, tick by tick, what must hold for any number of aircraft: nothing jumps or sinks into the
// ground, no two aircraft come close, a runway has one user at a time and is free before every touchdown.
type airWatch struct {
	t                      *testing.T
	a                      *AirTraffic
	label                  string
	previous               map[string]AircraftView
	phases                 map[string]float64 // aircraft-seconds per phase
	landings               map[string]int
	closest, closestInAir  float64
	closestPair            string
	longestWait            float64
	waiting                map[string]float64
	checkedFinals, airtime float64
}

func watchAir(t *testing.T, a *AirTraffic, label string) *airWatch {
	return &airWatch{t: t, a: a, label: label, previous: map[string]AircraftView{}, phases: map[string]float64{},
		landings: map[string]int{}, closest: math.Inf(1), closestInAir: math.Inf(1), waiting: map[string]float64{}}
}

func cruising(phase string) bool { return phase == "CLIMB" || phase == "CRUISE" || phase == "HOLDING" }

func (w *airWatch) tick(dt float64) {
	t, a := w.t, w.a
	for _, landed := range a.Tick(dt) {
		w.landings[landed.ID]++
		if landed.Phase != "LANDING" {
			t.Fatalf("%s: %s reported as landed in %s", w.label, landed.ID, landed.Phase)
		}
	}
	views := a.Views()
	inside := [2][]string{}
	for i, v := range views {
		pos := vecOf(v.Pos)
		w.phases[v.Phase] += dt
		if cruising(v.Phase) || v.Phase == "APPROACH" {
			w.airtime += dt
		}
		if v.Phase == "QUEUE" || v.Phase == "PARKED" {
			w.waiting[v.ID] += dt
			w.longestWait = math.Max(w.longestWait, w.waiting[v.ID])
		} else {
			w.waiting[v.ID] = 0
		}
		if was, seen := w.previous[v.ID]; seen {
			jump := pos.Sub(vecOf(was.Pos)).Length()
			if !isFinite(jump) || jump > 3.5*dt+1e-9 {
				t.Fatalf("%s: %s jumped %v in %s at %.1f s", w.label, v.ID, jump, v.Phase, a.clock)
			}
			if turn := math.Abs(angleBetween(was.Heading, v.Heading)); v.Gear && !cruising(was.Phase) && was.Phase != "APPROACH" && turn > airTurnRate*dt+1e-9 {
				t.Fatalf("%s: %s turned %v rad in one tick on the ground in %s", w.label, v.ID, turn, v.Phase)
			}
		}
		if v.Pos[1] < .099 || !isFinite(v.Heading) || !isFinite(v.Pitch) || !isFinite(v.Bank) || v.Speed > 3.5*25*3.6 {
			t.Fatalf("%s: %s at height %v, speed %v in %s", w.label, v.ID, v.Pos[1], v.Speed, v.Phase)
		}
		if v.Gear != !cruising(v.Phase) {
			t.Fatalf("%s: %s has gear %v in %s", w.label, v.ID, v.Gear, v.Phase)
		}
		if v.Origin == v.Destination || v.Origin == "" || v.Destination == "" {
			t.Fatalf("%s: %s flies %q to %q", w.label, v.ID, v.Origin, v.Destination)
		}
		w.previous[v.ID] = v
		for n, s := range a.strips {
			if !s.nearRunway(pos) || cruising(v.Phase) || v.Phase == "APPROACH" {
				continue
			}
			if v.Phase == "QUEUE" || v.Phase == "PARKED" {
				t.Fatalf("%s: %s waits inside the hold lines of %s", w.label, v.ID, s.airport.ID)
			}
			inside[n] = append(inside[n], v.ID+" "+v.Phase)
		}
		for _, other := range views[i+1:] {
			d := pos.Sub(vecOf(other.Pos)).Length()
			if d < w.closest {
				w.closest, w.closestPair = d, fmt.Sprintf("%s %s and %s %s at %.0f s", v.ID, v.Phase, other.ID, other.Phase, a.clock)
			}
			if cruising(v.Phase) && cruising(other.Phase) {
				w.closestInAir = math.Min(w.closestInAir, d)
			}
		}
	}
	for n, s := range a.strips {
		if len(inside[n]) > 1 {
			t.Fatalf("%s: %v share the runway of %s at %.1f s", w.label, inside[n], s.airport.ID, a.clock)
		}
		if s.conflicts > 0 {
			t.Fatalf("%s: a touchdown at %s on a runway in use at %.1f s", w.label, s.airport.ID, a.clock)
		}
		// On short final the runway is already free.
		for _, p := range a.planes {
			if p.state == airApproach && a.legs[p.strip].to == s && a.legs[p.strip].approachTime-p.t < airFinalGap-airMargin {
				w.checkedFinals += dt
				if len(inside[n]) > 0 {
					t.Fatalf("%s: %s is %.1f s from touchdown at %s with %v on the runway", w.label, p.id,
						a.legs[p.strip].approachTime-p.t, s.airport.ID, inside[n])
				}
			}
		}
	}
}

func (w *airWatch) run(seconds float64, steps ...float64) {
	for i := 0; w.a.clock < seconds; i++ {
		w.tick(steps[i%len(steps)])
	}
}

func TestAirTrafficUsesTheMappedLoopsAndSeparatedLevels(t *testing.T) {
	a := NewBundledAirTraffic(0)
	network := BundledNetwork()
	for n, s := range a.strips {
		var mapped [][]Vec3
		for _, id := range airLoops[s.airport.ID] {
			for _, way := range network.Airways {
				if way.ID == id {
					var points []Vec3
					for _, p := range way.Points {
						points = append(points, Vec3{p[0], 0, p[1]})
					}
					mapped = append(mapped, points)
				}
			}
		}
		if len(mapped) != len(airLoops[s.airport.ID]) {
			t.Fatalf("%s: %d of its taxiways found", s.airport.ID, len(mapped))
		}
		// Past the runway exit the path is the mapped taxiways, but for the rounded corners.
		for at := s.sExit + 1; at < s.sEnd-1; at += .1 {
			p, _ := along(s.path, s.cum, at)
			nearest := math.Inf(1)
			for _, way := range mapped {
				for i := 1; i < len(way); i++ {
					seg := way[i].Sub(way[i-1])
					share := clamp(p.Sub(way[i-1]).Dot(seg)/math.Max(seg.Dot(seg), 1e-12), 0, 1)
					nearest = math.Min(nearest, p.Sub(way[i-1].Add(seg.Mul(share))).Length())
				}
			}
			if nearest > .2 {
				t.Fatalf("%s: the path is %.2f units from the mapped loop at %.1f", s.airport.ID, nearest, at)
			}
		}
		if !(0 < s.sExit && s.sExit < s.sClear && s.sClear+float64(MaxAircraft)*airQueueGap <= s.sHold && s.sHold < s.sEnd) {
			t.Fatalf("%s: path marks %v %v %v %v", s.airport.ID, s.sExit, s.sClear, s.sHold, s.sEnd)
		}
		hold, _ := along(s.path, s.cum, s.sHold)
		clear, _ := along(s.path, s.cum, s.sClear)
		for _, p := range []Vec3{hold, clear} {
			if _, lateral := s.offset(p); math.Abs(math.Abs(lateral)-airHoldLateral) > .1 {
				t.Fatalf("%s: a hold line %.2f units from the centre line", s.airport.ID, lateral)
			}
		}
		leg := a.legs[n]
		// The approach is longer than the time the runway is kept free before a touchdown.
		if leg.climbTime < 15 || leg.approachTime <= airFinalGap || leg.cruiseCum[len(leg.cruiseCum)-1] < 100 {
			t.Fatalf("%s: climb %v s, approach %v s, cruise %v", s.airport.ID, leg.climbTime, leg.approachTime, leg.cruiseCum[len(leg.cruiseCum)-1])
		}
	}
	// The two directions never come nearer than their levels are apart.
	nearest := math.Inf(1)
	for _, p := range a.legs[0].cruise {
		for _, q := range a.legs[1].cruise {
			nearest = math.Min(nearest, p.Sub(q).Length())
		}
	}
	if levels := math.Abs(a.strips[0].level - a.strips[1].level); levels < 4 || nearest < levels-1e-6 {
		t.Fatalf("levels %v apart, cruise tracks %v apart", levels, nearest)
	}
	if a.rounds < 2 || a.period < a.legs[0].separation || a.period < a.legs[1].separation {
		t.Fatalf("timetable of %d rounds, period %v", a.rounds, a.period)
	}
}

func TestAirTrafficKeepsEveryFleetApartAndMoving(t *testing.T) {
	const hours = 6
	completed := make([]int, MaxAircraft+1)
	for n := 1; n <= MaxAircraft; n++ {
		for _, steps := range [][]float64{{.05}, {.02}, {.05, .05, .013}} {
			if testing.Short() && (n%3 != 0 || len(steps) > 1 || steps[0] != .05) {
				continue
			}
			a := NewBundledAirTraffic(n)
			label := fmt.Sprintf("%d aircraft, steps %v", n, steps)
			w := watchAir(t, a, label)
			if len(a.Views()) != n {
				t.Fatalf("%s: %d aircraft at the start", label, len(a.Views()))
			}
			w.run(hours*3600, steps...)
			if steps[0] == .05 && len(steps) == 1 {
				completed[n] = a.Completed
			}
			for _, phase := range []string{"PARKED", "TAXI_OUT", "TAKEOFF", "CLIMB", "CRUISE", "APPROACH", "LANDING", "TAXI_IN"} {
				if w.phases[phase] == 0 {
					t.Fatalf("%s: never in %s (%v)", label, phase, w.phases)
				}
			}
			// Two aircraft never meet at a hold line; from three on there is a queue now and then.
			if n > 2 && w.phases["QUEUE"] == 0 {
				t.Fatalf("%s: nobody ever queued", label)
			}
			// Nobody has to hold in the air: the clearance rules keep the runways free in time.
			if w.phases["HOLDING"] != 0 {
				t.Fatalf("%s: %v s of holding", label, w.phases["HOLDING"])
			}
			for _, v := range a.Views() {
				if w.landings[v.ID] < hours*2 {
					t.Fatalf("%s: %s landed %d times in %d h (%v)", label, v.ID, w.landings[v.ID], hours, w.landings)
				}
			}
			if n > 1 && (w.closest < 1.5 || w.closestInAir < 4) {
				t.Fatalf("%s: %.2f units apart (%s), %.2f in the air", label, w.closest, w.closestPair, w.closestInAir)
			}
			if w.longestWait > 900 || w.checkedFinals == 0 {
				t.Fatalf("%s: an aircraft waited %.0f s; finals checked for %.0f s", label, w.longestWait, w.checkedFinals)
			}
			t.Logf("%s: %d flights, %.2f in the air on average, closest %.2f (in the air %.2f), longest wait %.0f s",
				label, a.Completed, w.airtime/(hours*3600), w.closest, w.closestInAir, w.longestWait)
		}
	}
	if !testing.Short() {
		for n := 2; n <= MaxAircraft; n++ {
			if completed[n] <= completed[n-1] {
				t.Fatalf("%d aircraft complete %d flights, %d aircraft %d", n, completed[n], n-1, completed[n-1])
			}
		}
	}
}

func TestAirTrafficIsDeterministic(t *testing.T) {
	a, b := NewBundledAirTraffic(MaxAircraft), NewBundledAirTraffic(MaxAircraft)
	for i := 0; i < 40000; i++ {
		a.Tick(.05)
		b.Tick(.05)
	}
	if !reflect.DeepEqual(a.Views(), b.Views()) || a.Completed != b.Completed || a.Completed == 0 {
		t.Fatalf("two runs differ: %d and %d flights", a.Completed, b.Completed)
	}
}

func TestAirTrafficStartsLikeTheSingleShuttle(t *testing.T) {
	a, legacy := NewBundledAirTraffic(MaxAircraft), NewBundledRegionalFlight()
	views := a.Views()
	for i, v := range views {
		home := legacy.fields[i%2].airport.Name
		if v.ID != fmt.Sprintf("NK-%02d", i+1) || v.Phase != "PARKED" || v.Origin != home || !v.Gear || v.Speed != 0 {
			t.Fatalf("aircraft %d starts as %+v", i, v)
		}
	}
	// The first leaves after the same ten seconds on the ground.
	for a.clock < airFirstReady+1 {
		a.Tick(.05)
	}
	if v := a.Views()[0]; v.Phase != "TAXI_OUT" {
		t.Fatalf("NK-01 is in %s after %.0f s", v.Phase, a.clock)
	}
}

func TestAirTrafficFollowsAChangedFleetSize(t *testing.T) {
	a := NewBundledAirTraffic(2)
	w := watchAir(t, a, "changing fleet")
	at := 0.0
	for _, n := range []int{5, 1, 6, 0, 3, 6, 2, 4} {
		a.SetCount(n)
		if len(a.planes) > n {
			t.Fatalf("%d aircraft after reducing to %d", len(a.planes), n)
		}
		// Removed aircraft are forgotten by the watch as well.
		for id := range w.previous {
			found := false
			for _, v := range a.Views() {
				found = found || v.ID == id
			}
			if !found {
				delete(w.previous, id)
			}
		}
		at += 1700
		w.run(at, .05)
		if len(a.Views()) != n {
			t.Fatalf("%d aircraft %0.f s after asking for %d", len(a.Views()), a.clock, n)
		}
		for i, v := range a.Views() {
			if v.ID != fmt.Sprintf("NK-%02d", i+1) {
				t.Fatalf("aircraft %d is %s", i, v.ID)
			}
		}
		for _, s := range a.strips {
			for _, slot := range s.inbound {
				present := false
				for _, p := range a.planes {
					present = present || p == slot.plane
				}
				// An arrival is expected until it has vacated the runway, a departure from its clearance on.
				if !present || (slot.plane.state == airGround && !slot.plane.cleared && s.runway != slot.plane) {
					t.Fatalf("%s expects %s, which is gone or parked", s.airport.ID, slot.plane.id)
				}
			}
		}
	}
	if a.Completed < 20 {
		t.Fatalf("%d flights", a.Completed)
	}
}

func TestAnArrivalHoldsWhileTheRunwayIsTaken(t *testing.T) {
	a := NewBundledAirTraffic(1)
	w := watchAir(t, a, "holding")
	for a.Views()[0].Phase != "CRUISE" {
		w.tick(.05)
	}
	// Something that the rules did not foresee blocks the destination runway.
	blocker := &aircraft{id: "blocker", freeAt: math.Inf(1)}
	a.strips[1].runway = blocker
	for a.Views()[0].Phase != "HOLDING" {
		w.tick(.05)
		if a.clock > 2000 {
			t.Fatal("the arrival never held")
		}
	}
	lap := 2 * math.Pi * airHoldRadius / airCruiseSpeed
	started := a.clock
	fix := a.legs[0].fix
	for a.clock < started+2.5*lap {
		w.tick(.05)
		v := a.Views()[0]
		if v.Phase != "HOLDING" || v.Gear {
			t.Fatalf("%s with gear %v while the runway is taken", v.Phase, v.Gear)
		}
		if d := vecOf(v.Pos).Sub(a.legs[0].orbitCentre).Length(); math.Abs(d-airHoldRadius) > 1e-6 || v.Pos[1] != fix.Y {
			t.Fatalf("holding %.3f units from the centre at height %v", d, v.Pos[1])
		}
	}
	// A queue at the airport it came from may not send more aircraft into the hold.
	a.SetCount(3)
	a.strips[1].runway = nil
	for a.Views()[0].Phase == "HOLDING" {
		w.tick(.05)
	}
	if v := a.Views()[0]; v.Phase != "APPROACH" || vecOf(v.Pos).Sub(fix).Length() > .2 {
		t.Fatalf("left the hold in %s, %.2f units from the fix", v.Phase, vecOf(v.Pos).Sub(fix).Length())
	}
	if waited := a.clock - started; waited > 3*lap+.1 {
		t.Fatalf("held %.0f s for a runway freed after %.0f s", waited, 2.5*lap)
	}
	w.run(a.clock+3600, .05)
	if w.landings["NK-01"] < 2 || len(a.strips[1].holding)+len(a.strips[0].holding) != 0 {
		t.Fatalf("after the hold: %v", w.landings)
	}
}

func TestCityFliesTheChosenNumberOfAircraft(t *testing.T) {
	city := NewCityService()
	if got := city.World.Value(); len(got.Aircraft) != 3 || got.Settings.Planes != 3 || got.Aircraft[2].ID != "NK-03" {
		t.Fatalf("a new city has %d aircraft", len(got.Aircraft))
	}
	settings := city.World.Value().Settings
	settings.Planes, settings.TimeScale = MaxAircraft, 20
	if err := city.Execute(ConfigureCity{settings}); err != nil {
		t.Fatal(err)
	}
	if got := city.World.Value(); len(got.Aircraft) != MaxAircraft {
		t.Fatalf("%d aircraft right after asking for %d", len(got.Aircraft), MaxAircraft)
	}
	for i := 0; i < 600; i++ {
		city.Step(.1)
	}
	got := city.World.Value()
	landed := map[string]bool{}
	for _, event := range got.Events {
		if event.Kind == "landing" {
			for _, v := range got.Aircraft {
				if strings.Contains(event.Message, ": "+v.ID+" har landat.") {
					landed[v.ID] = true
				}
			}
		}
	}
	if got.CompletedFlights < 8 || len(landed) < 4 {
		t.Fatalf("%d flights, landings logged for %v", got.CompletedFlights, landed)
	}
	settings.Planes = MaxAircraft + 1
	if err := city.Execute(ConfigureCity{settings}); !IsValidation(err) || err.Error() != "Välj högst 6 flygplan." {
		t.Fatalf("too many aircraft: %v", err)
	}
	settings.Planes = -1
	if err := city.Execute(ConfigureCity{settings}); !IsValidation(err) {
		t.Fatalf("a negative number of aircraft: %v", err)
	}
	settings.Planes, settings.Flights = 4, false
	if err := city.Execute(ConfigureCity{settings}); err != nil {
		t.Fatal(err)
	}
	city.Step(.1)
	if got := city.World.Value(); len(got.Aircraft) != 0 {
		t.Fatalf("%d aircraft with flights off", len(got.Aircraft))
	}
	settings.Flights = true
	city.Execute(ConfigureCity{settings})
	if err := city.Execute(ResetCity{}); err != nil {
		t.Fatal(err)
	}
	if got := city.World.Value(); len(got.Aircraft) != 4 || got.CompletedFlights != 0 || got.Aircraft[0].Phase != "PARKED" {
		t.Fatalf("after a reset: %d aircraft, %d flights", len(got.Aircraft), got.CompletedFlights)
	}
	// A client that does not name a number keeps the single shuttle of the earlier versions.
	settings.Planes = 0
	city.Execute(ConfigureCity{settings})
	city.Step(.1)
	if got := city.World.Value(); len(got.Aircraft) != 1 || got.Aircraft[0].ID != "NK-01" ||
		!reflect.DeepEqual(got.Aircraft[0].Pos, NewBundledRegionalFlight().View().Pos) {
		t.Fatalf("without a number: %+v", got.Aircraft)
	}
}

// TestWriteAirTrafficGeometry writes the ground paths, flight tracks and timetable to the file named by
// AIR_GEOMETRY_OUT: the UI repository keeps it as docs/air-traffic/data/geometry.json and checks the paths
// against the mapped buildings (scripts/air-traffic/clearance.mjs). Scene units, X east and Z south.
func TestWriteAirTrafficGeometry(t *testing.T) {
	out := os.Getenv("AIR_GEOMETRY_OUT")
	if out == "" {
		t.Skip("AIR_GEOMETRY_OUT is not set")
	}
	a := NewBundledAirTraffic(0)
	round := func(v float64) float64 { return math.Round(v*1e4) / 1e4 }
	flat := func(points []Vec3, every int) [][]float64 {
		var list [][]float64
		for i := 0; i < len(points); i += every {
			list = append(list, []float64{round(points[i].X), round(points[i].Z)})
		}
		return list
	}
	type strip struct {
		ID, Name                                  string
		Taxiways                                  []int64
		Start, Direction                          []float64
		Length, Width                             float64
		Path                                      [][]float64
		PathExit, PathClear, PathHold, PathEnd    float64
		Touchdown, RolloutEnd, LiftOff            float64
		DepartureLevel, LineUpSeconds             float64
		VacateSeconds, FirstSlotSeconds           float64
		ClimbSeconds, CruiseUnits, ApproachSecond float64
		FlightSeconds, TouchdownSeparationSeconds float64
		ClimbEnd, Fix, HoldCentre                 []float64
		Cruise                                    [][]float64
	}
	var strips []strip
	for i, s := range a.strips {
		l := a.legs[i]
		strips = append(strips, strip{s.airport.ID, s.airport.Name, airLoops[s.airport.ID],
			[]float64{round(s.start.X), round(s.start.Z)}, []float64{round(s.dir.X), round(s.dir.Z)},
			round(s.length), s.airport.Width, flat(s.path, 1), round(s.sExit), round(s.sClear), round(s.sHold), round(s.sEnd),
			round(s.touch), round(s.rollEnd), round(s.launch), s.level, round(s.lineUp), round(s.vacate), round(s.slot),
			round(l.climbTime), round(l.cruiseCum[len(l.cruiseCum)-1]), round(l.approachTime), round(l.flightTime), round(l.separation),
			[]float64{round(l.climbEnd.X), round(l.climbEnd.Z)}, []float64{round(l.fix.X), round(l.fix.Z)},
			[]float64{round(l.orbitCentre.X), round(l.orbitCentre.Z)}, flat(l.cruise, 8)})
	}
	raw, err := json.MarshalIndent(map[string]any{
		"unitMetres": 25, "maxAircraft": MaxAircraft, "timetablePeriodSeconds": round(a.period), "timetableRounds": a.rounds,
		"queueGap": airQueueGap, "holdLateral": airHoldLateral, "holdRadius": airHoldRadius, "strips": strips,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkAirTrafficTick is one 0.05 s tick of the largest fleet.
func BenchmarkAirTrafficTick(b *testing.B) {
	a := NewBundledAirTraffic(MaxAircraft)
	for i := 0; i < 20000; i++ {
		a.Tick(.05)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Tick(.05)
	}
}

// Without the timetable the two runways fall out of step and the largest fleet completes fewer flights;
// with it, no fewer than a fleet of that size can.
func TestTimetableKeepsTheLargestFleetInStep(t *testing.T) {
	const seconds = 6 * 3600
	flights := func(timetable bool, count int) int {
		a := NewBundledAirTraffic(count)
		if !timetable {
			a.rounds = MaxAircraft // every fleet is then small enough to leave as soon as it may
		}
		w := watchAir(t, a, fmt.Sprintf("%d aircraft, timetable %v", count, timetable))
		w.run(seconds, .05)
		return a.Completed
	}
	with, without := flights(true, MaxAircraft), flights(false, MaxAircraft)
	t.Logf("%d aircraft in 6 h: %d flights with the timetable, %d without", MaxAircraft, with, without)
	t.Logf("%d aircraft in 6 h: %d flights with the timetable, %d without", MaxAircraft-1, flights(true, MaxAircraft-1), flights(false, MaxAircraft-1))
	a := NewBundledAirTraffic(0)
	// One flight per period in each direction is all two runways can take.
	most := int(2 * seconds / a.period)
	if with <= without || with < most*95/100 || with > most {
		t.Fatalf("%d flights with the timetable, %d without, at most %d possible", with, without, most)
	}
}
