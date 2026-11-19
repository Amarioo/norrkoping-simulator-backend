package sim

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
)

// AirTraffic flies up to MaxAircraft fictional shuttles between the two airports, one runway each.
//
// The ground layout uses mapped geometry only: at each runway's take-off end OpenStreetMap has taxiways
// that leave the runway a little way down and return to the threshold in a loop (airLoops). Traffic goes
// one way round it: a landed aircraft turns on the runway, taxis back to the loop's far end, follows the loop
// and joins the queue behind the hold line; the first in the queue lines up and takes off. Nothing is ever
// head to head, so there is no deadlock to resolve, and no parking stand had to be invented.
//
// Rules:
//   - A runway has one user at a time: an arrival from touchdown until it has left the runway for the
//     loop, a departure from the hold line until it is airborne. It is free at least airFinalGap seconds
//     before every touchdown.
//   - A departure is cleared only if it is airborne that long before the next touchdown at its own
//     airport, and if its own touchdown comes late enough after those already under way to the other
//     airport for the runway there to be vacated and one departure to leave in between. That keeps both
//     queues moving.
//   - An arrival that would still find the runway taken flies a holding circle at the approach fix and
//     asks again each lap. The clearance rule makes this rare; it is what keeps the rule from having to
//     be exact.
//   - With more aircraft than fit one round of flights, departures leave in the slots of a timetable
//     (timetable): otherwise the two runways fall out of step.
//   - The two directions cruise at different levels (airLevels), because their tracks cross. Each
//     flight takes one of several routes (airVias), drawn at random when it is cleared; every route lies
//     inside the mapped area, and a longer one is flown faster, so that all flights between the airports
//     take the same time and the rules above need not know the route.
//   - Each aircraft is of a type drawn at random when it appears (airKinds).
//
// Speeds, times, distances and levels below are ESTIMATES chosen for a plausible picture in the 1:25 scene
// (1 unit = 25 m); none is a measured procedure of either airport.
type AirTraffic struct {
	terrain   *TerrainService
	rng       *rand.Rand
	clip      []Vec3 // outline of the mapped area
	strips    [2]*airStrip
	period    float64 // of the timetable, see timetable()
	rounds    int     // flights that fit one round trip of the timetable
	legs      [2]*airLeg
	planes    []*aircraft
	want      int
	clock     float64
	Completed int
}

// MaxAircraft is the most aircraft that fit the two taxiway loops with room for an arrival behind them.
const MaxAircraft = 6

const (
	airTaxiSpeed      = .4   // on taxiways: 10 m/s
	airBacktrackSpeed = .7   // back along the runway after landing: 17.5 m/s
	airQueueGap       = 2.4  // nose to nose in the queue: 60 m (the longest type is 44.5 m)
	airWingRoom       = .8   // room kept between the path and a building beside it: 20 m (widest half span 18 m)
	airMapMargin      = 2.0  // every flight stays this far inside the mapped area: 50 m
	airHoldLateral    = 2.0  // hold line and "runway vacated": 50 m from the centre line
	airPivotRate      = .35  // turning on the spot, rad/s (20°/s)
	airTurnRate       = 1.2  // largest heading change while rolling, rad/s
	airPivotAngle     = .6   // a larger heading error than this is turned on the spot
	airTurnaround     = 45.0 // on the ground in the queue before it may ask to leave, s
	airFirstReady     = 10.0 // the same for an aircraft that starts in the queue (as the single shuttle)
	airRollTime       = 35.0 // take-off roll, s
	airLiftoffSpeed   = 2.2  // 55 m/s at the end of the roll, and the horizontal speed of the climb
	airClimbRate      = .36  // mean, 9 m/s
	airSinkRate       = .22  // on the approach, 5.5 m/s
	airApproachSpeed  = 2.5  // horizontal, 62.5 m/s
	airTouchdown      = .12  // touchdown point as a share of the runway (as the single shuttle)
	airRolloutTime    = 20.0 // from touchdown to walking pace, s
	airCruiseSpeed    = 2.55 // 64 m/s on the direct route; at this speed four flights fit one round of the timetable
	airSlowest        = 2.3  // no route is flown slower than this, 57.5 m/s,
	airFastest        = 3.3  // or faster than this, 82.5 m/s
	airTurnRadius     = 20.0 // turns of the cruise, 500 m
	airApproachLevel  = 6.0  // level at the approach fix, 150 m: the cruise ends with a descent to it
	airHoldRadius     = 24.0 // holding circle, 600 m
	airFinalGap       = 20.0 // the runway is free this long before a touchdown, s
	airMargin         = 8.0  // slack in the predictions, s
	airSlotWindow     = 5.0  // how late in its slot a departure may still be cleared, s
)

// airKind is a type of aircraft. Length and span in metres are the manufacturers' published figures; the
// interface draws the model. Which types are in the list is a choice, see docs/air-routes in the UI
// repository: a 737 MAX 8 and an A321neo have flown charters from Norrköping, the A320neo was asked for,
// and "TP" is the unnamed turboprop of the earlier versions.
type airKind struct {
	ID           string
	Length, Span float64
}

var airKinds = []airKind{
	{"TP", 19, 23},
	{"A20N", 37.57, 35.80},
	{"A21N", 44.51, 35.80},
	{"B38M", 39.52, 35.92},
}

// airVia is a route for departures from an airport: its name (shown in the interface) and the points
// it passes between the climb and the approach fix of the other airport, in scene coordinates (x, z).
// ESTIMATES: no published procedure is behind them.
type airVia struct {
	name string
	via  [][2]float64
}

var airVias = map[string][]airVia{
	"ESSP": {
		{"över staden", nil},
		{"norr om staden", [][2]float64{{-20, -150}, {-150, -25}}},
		{"söder om staden", [][2]float64{{0, 110}, {-170, 40}}},
	},
	"F13": {
		{"över staden", nil},
		{"norr om staden", [][2]float64{{-70, -150}, {0, -40}}},
		{"söder om staden", [][2]float64{{-110, 20}}},
	},
}

// airObstacles are the points of mapped buildings nearest the ground path where a wide wing would not
// pass (scene x, z; OpenStreetMap ways 104336425 and 104336441 at Bråvalla). The path bends away from
// them until airWingRoom is left, by at most a third of a unit (8 m) off the mapped centre line.
var airObstacles = map[string][][2]float64{"F13": {{-223.184, -63.997}, {-226.987, -63.655}}}

// keepClear bends a path away from obstacles: within 2.5 units of one, points move straight away from
// it, most where the path is nearest, so that `room` is left there.
func keepClear(path []Vec3, obstacles [][2]float64, room float64) []Vec3 {
	if len(obstacles) == 0 {
		return path
	}
	dense := []Vec3{path[0]}
	for i := 1; i < len(path); i++ {
		steps := int(math.Ceil(path[i].Sub(path[i-1]).Length() / .2))
		for k := 1; k <= steps; k++ {
			dense = append(dense, path[i-1].Add(path[i].Sub(path[i-1]).Mul(float64(k)/float64(steps))))
		}
	}
	const reach = 2.5
	weight := func(d float64) float64 {
		w := clamp(1-d/reach, 0, 1)
		return w * w * (3 - 2*w)
	}
	for _, o := range obstacles {
		at := Vec3{o[0], 0, o[1]}
		nearest := math.Inf(1)
		for _, p := range dense {
			nearest = math.Min(nearest, p.Sub(at).Length())
		}
		if nearest >= room || nearest < 1e-6 {
			continue
		}
		push := (room - nearest) / weight(nearest)
		for i, p := range dense {
			if d := p.Sub(at).Length(); d < reach && d > 1e-6 {
				dense[i] = p.Add(p.Sub(at).Mul(push * weight(d) / d))
			}
		}
	}
	return dense
}

// airLoops names the taxiways that make up the loop at each airport, in the order they are driven
// (OpenStreetMap way ids, © OpenStreetMap contributors). The first leaves the runway; each next one starts
// where one of its ends meets the one before; the last ends on the threshold.
//
// At Bråvalla way 104336414 would be the whole loop, but it runs through two of the mapped buildings
// there, which are drawn as solid houses. The two ways used instead keep clear of every
// building; where they meet, at an acute angle, the aircraft turns nearly on the spot.
var airLoops = map[string][]int64{"ESSP": {101516378}, "F13": {104336417, 104336403}}

// airLoop joins the taxiways of airLoops into one polyline from the runway to the threshold.
func airLoop(network *TransportNetwork, s *airStrip) ([]Vec3, error) {
	var loop []Vec3
	for n, id := range airLoops[s.airport.ID] {
		var way []Vec3
		for i := range network.Airways {
			if w := &network.Airways[i]; w.ID == id && w.Kind == "taxiway" {
				for _, p := range w.Points {
					way = append(way, Vec3{p[0], 0, p[1]})
				}
			}
		}
		if len(way) < 2 {
			return nil, fmt.Errorf("taxiway %d of the loop at %s is missing", id, s.airport.ID)
		}
		reverse := func() {
			for i, j := 0, len(way)-1; i < j; i, j = i+1, j-1 {
				way[i], way[j] = way[j], way[i]
			}
		}
		if n == 0 {
			// It starts at its end on the runway; with both ends there, at the one further down.
			first, firstSide := s.offset(way[0])
			last, lastSide := s.offset(way[len(way)-1])
			onRunway := func(side float64) bool { return math.Abs(side) <= s.airport.Width/2 }
			if onRunway(lastSide) && (!onRunway(firstSide) || last > first) {
				reverse()
			}
			loop = way
			continue
		}
		// Cut the loop so far where the next taxiway begins.
		joined := false
		for i := range loop {
			if loop[i].Sub(way[len(way)-1]).Length() < .05 {
				reverse()
			}
			if loop[i].Sub(way[0]).Length() < .05 {
				loop, joined = append(loop[:i:i], way...), true
				break
			}
		}
		if !joined {
			return nil, fmt.Errorf("taxiway %d does not meet the loop at %s", id, s.airport.ID)
		}
	}
	if len(loop) < 3 {
		return nil, fmt.Errorf("no taxiway loop for airport %s", s.airport.ID)
	}
	return loop, nil
}

// airLevels is the cruise level of departures from each airport, in scene units (300 m and 200 m). Flights
// from Norrköpings flygplats are the higher ones: the others approach it under their track.
var airLevels = map[string]float64{"ESSP": 12, "F13": 8}

const (
	airGround = iota // on the loop, in the queue or lining up
	airRoll
	airClimb
	airCruise
	airOrbit
	airApproach
	airRollout
)

// airSlot is an arrival under way to a strip, with its predicted time of touchdown.
type airSlot struct {
	plane *aircraft
	touch float64
}

// airStrip is one runway with its ground path, in the horizontal plane (Y is taken from the terrain).
type airStrip struct {
	airport *Airport
	start   Vec3 // the take-off threshold
	dir     Vec3 // unit, the direction of take-off and landing
	heading float64
	length  float64
	// path runs from where the landing roll ends, back along the centre line to the loop, round the loop
	// and onto the threshold. cum is the distance along it.
	path                       []Vec3
	cum                        []float64
	sExit, sClear, sHold, sEnd float64
	touch, rollEnd, launch     float64 // along the runway from start
	level                      float64
	slot                       float64 // clock time of the first departure slot of the timetable
	lineUp                     float64 // seconds from the hold line to the threshold
	vacate                     float64 // seconds from touchdown until the runway is vacated
	conflicts                  int     // touchdowns on a runway in use; the rules keep this at 0
	runway                     *aircraft
	inbound                    []*airSlot
	holding                    []*aircraft
}

// airLeg is the flight from one strip to the other.
type airLeg struct {
	from, to       *airStrip
	launch         Vec3 // where the roll ends, on the ground
	climbTime      float64
	climbEnd       Vec3
	routes         []*airRoute
	cruiseTime     float64 // the same on every route
	fix            Vec3    // end of the cruise, start of the approach
	orbitCentre    Vec3
	approachTime   float64
	touch, rollEnd Vec3
	flightTime     float64 // from the start of the roll to touchdown
	separation     float64 // least time between two touchdowns at the destination
}

// airRoute is one way to fly a leg: from the end of the climb to the approach fix.
type airRoute struct {
	name   string
	cruise []Vec3
	cum    []float64
	speed  float64 // length / the leg's cruise time
}

type aircraft struct {
	id      string
	kind    int // index in airKinds
	route   int // index in the leg's routes, drawn when it is cleared
	strip   int // on the ground: where it is; in the air: where it took off
	state   int
	s       float64 // ground: distance along the strip's path
	t       float64 // seconds into the current flight phase
	arrived bool    // has taken its place in the queue
	ready   float64 // clock time from which it may ask for take-off clearance
	cleared bool
	freeAt  float64 // while it has the runway: the predicted time it leaves it
	stepped bool    // already advanced in this tick
	slot    *airSlot
	heading float64
	last    Vec3
	view    AircraftView
}

func (a *AirTraffic) ground(p Vec3) Vec3 {
	return Vec3{p.X, a.terrain.PlaceLegacy(p.X, p.Z, .1, runwaySurface), p.Z}
}

func flat(p Vec3) Vec3 { return Vec3{p.X, 0, p.Z} }

func (s *airStrip) at(along float64) Vec3 { return s.start.Add(s.dir.Mul(along)) }

// offset is a point's position relative to the runway: along it from the threshold, and to the side.
func (s *airStrip) offset(p Vec3) (along, lateral float64) {
	rel := flat(p).Sub(s.start)
	return rel.Dot(s.dir), rel.X*s.dir.Z - rel.Z*s.dir.X
}

// nearRunway reports whether an aircraft at p is inside the hold lines.
func (s *airStrip) nearRunway(p Vec3) bool {
	along, lateral := s.offset(p)
	return math.Abs(lateral) < airHoldLateral && along > -8 && along < s.length+8
}

// roundCorners replaces each corner sharper than 20° by a short curve, so that an aircraft turns while
// rolling instead of stopping to pivot.
func roundCorners(points []Vec3, cut float64) []Vec3 {
	out := []Vec3{points[0]}
	for i := 1; i < len(points)-1; i++ {
		in, away := points[i].Sub(points[i-1]), points[i+1].Sub(points[i])
		turn := math.Acos(clamp(in.Normalized().Dot(away.Normalized()), -1, 1))
		d := math.Min(cut, math.Min(in.Length(), away.Length())/2)
		if turn < .35 || d < .05 {
			out = append(out, points[i])
			continue
		}
		p, q := points[i].Sub(in.Normalized().Mul(d)), points[i].Add(away.Normalized().Mul(d))
		for k := 0; k <= 6; k++ {
			t := float64(k) / 6
			out = append(out, p.Mul((1-t)*(1-t)).Add(points[i].Mul(2*(1-t)*t)).Add(q.Mul(t*t)))
		}
	}
	return append(out, points[len(points)-1])
}

func cumulative(points []Vec3) []float64 {
	cum := make([]float64, len(points))
	for i := 1; i < len(points); i++ {
		cum[i] = cum[i-1] + points[i].Sub(points[i-1]).Length()
	}
	return cum
}

// along returns the point at distance s on a polyline and the direction of travel there.
func along(points []Vec3, cum []float64, s float64) (Vec3, Vec3) {
	s = clamp(s, 0, cum[len(cum)-1])
	i := sort.SearchFloat64s(cum, s)
	if i < len(cum) && cum[i] == s && i < len(cum)-1 {
		i++
	}
	i = clampInt(i, 1, len(points)-1)
	seg := points[i].Sub(points[i-1])
	return points[i-1].Add(seg.Mul((s - cum[i-1]) / math.Max(cum[i]-cum[i-1], 1e-9))), seg.Normalized()
}

func (a *AirTraffic) strip(network *TransportNetwork, airport *Airport) (*airStrip, error) {
	start, end := Vec3{airport.Start[0], 0, airport.Start[1]}, Vec3{airport.End[0], 0, airport.End[1]}
	s := &airStrip{airport: airport, start: start, dir: end.Sub(start).Normalized(), length: end.Sub(start).Length()}
	s.heading = math.Atan2(s.dir.X, s.dir.Z)
	level, known := airLevels[airport.ID]
	if !known {
		return nil, fmt.Errorf("no cruise level for airport %s", airport.ID)
	}
	s.level = level
	s.touch = s.length * airTouchdown
	s.rollEnd = s.touch + airApproachSpeed*airRolloutTime/2
	s.launch = airLiftoffSpeed * airRollTime / 2
	loop, err := airLoop(network, s)
	if err != nil {
		return nil, err
	}
	exitAlong, exitLateral := s.offset(loop[0])
	if loop[len(loop)-1].Sub(start).Length() > .5 || math.Abs(exitLateral) > airport.Width/2 ||
		exitAlong < 1 || exitAlong > s.rollEnd-1 || s.rollEnd > s.length-1 || s.launch > s.length-1 {
		return nil, fmt.Errorf("the taxiway loop at %s does not fit its runway", airport.ID)
	}
	// The loop's last points lie on the runway next to the threshold; the path ends on the threshold itself.
	for len(loop) > 1 && loop[len(loop)-1].Sub(start).Length() < .3 {
		loop = loop[:len(loop)-1]
	}
	path := append([]Vec3{s.at(s.rollEnd), s.at(exitAlong)}, loop[1:]...)
	s.path = keepClear(roundCorners(append(path, start), .8), airObstacles[airport.ID], airWingRoom)
	s.cum = cumulative(s.path)
	s.sExit, s.sEnd = s.rollEnd-exitAlong, s.cum[len(s.cum)-1]
	s.sClear, s.sHold = s.sExit, s.sEnd
	for p, _ := along(s.path, s.cum, s.sClear); s.nearRunway(p) && s.sClear < s.sEnd; p, _ = along(s.path, s.cum, s.sClear) {
		s.sClear += .05
	}
	for p, _ := along(s.path, s.cum, s.sHold); s.nearRunway(p) && s.sHold > s.sClear; p, _ = along(s.path, s.cum, s.sHold) {
		s.sHold -= .05
	}
	s.lineUp = (s.sEnd - s.sHold) / airTaxiSpeed
	s.vacate = airRolloutTime + math.Pi/airPivotRate + s.sExit/airBacktrackSpeed + (s.sClear-s.sExit)/airTaxiSpeed + airMargin
	if s.sHold-float64(MaxAircraft)*airQueueGap < s.sClear {
		return nil, fmt.Errorf("the taxiway loop at %s is too short for %d aircraft", airport.ID, MaxAircraft)
	}
	return s, nil
}

// turnStraightTurn is the shortest level path that leaves `from` in direction `out` and reaches `to` in
// direction `in` by a turn of the given radius, a straight and another turn (a Dubins path), as points
// about a unit apart. Directions are unit vectors in the horizontal plane; X is east and Z south, so a
// right turn takes the heading angle atan2(z, x) upwards.
func turnStraightTurn(from, out, to, in Vec3, radius float64) []Vec3 {
	right := func(d Vec3) Vec3 { return Vec3{-d.Z, 0, d.X} }
	angle := func(d Vec3) float64 { return math.Atan2(d.Z, d.X) }
	// sweep is how far a turn to one side goes round from heading a to heading b.
	sweep := func(a, b, side float64) float64 {
		return math.Mod(math.Mod(side*(b-a), 2*math.Pi)+2*math.Pi, 2*math.Pi)
	}
	var best []Vec3
	shortest := math.Inf(1)
	for _, first := range []float64{1, -1} { // 1 a right turn, -1 a left turn
		for _, last := range []float64{1, -1} {
			c1, c2 := from.Add(right(out).Mul(first*radius)), to.Add(right(in).Mul(last*radius))
			between := c2.Sub(c1)
			between.Y = 0
			span := between.Length()
			var along Vec3
			if first == last {
				if span < 1e-9 {
					continue
				}
				along = between.Mul(1 / span)
			} else {
				// The straight crosses between the circles: between + (first-last)·radius·right(along)
				// is parallel to along.
				if span < 2*radius {
					continue
				}
				base, tilt := math.Atan2(between.Z, between.X), math.Asin(2*radius/span)
				for _, candidate := range []float64{base + tilt, base - tilt} {
					d := Vec3{math.Cos(candidate), 0, math.Sin(candidate)}
					if math.Abs(between.Dot(right(d))+first*2*radius) < 1e-6*span {
						along = d
					}
				}
				if along.Length() == 0 {
					continue
				}
			}
			leave, join := c1.Sub(right(along).Mul(first*radius)), c2.Sub(right(along).Mul(last*radius))
			turn1, turn2 := sweep(angle(out), angle(along), first), sweep(angle(along), angle(in), last)
			length := radius*(turn1+turn2) + join.Sub(leave).Length()
			if length >= shortest {
				continue
			}
			shortest = length
			arc := func(centre Vec3, start, round, side float64) []Vec3 {
				var points []Vec3
				steps := int(math.Ceil(round * radius))
				for i := 0; i <= steps; i++ {
					a := start
					if steps > 0 {
						a += side * round * float64(i) / float64(steps)
					}
					d := Vec3{math.Cos(a), 0, math.Sin(a)}
					points = append(points, centre.Sub(right(d).Mul(side*radius)))
				}
				return points
			}
			best = arc(c1, angle(out), turn1, first)
			straight := join.Sub(leave)
			for i, steps := 1, int(math.Ceil(straight.Length())); i < steps; i++ {
				best = append(best, leave.Add(straight.Mul(float64(i)/float64(steps))))
			}
			best = append(best, arc(c2, angle(along), turn2, last)...)
		}
	}
	for i := range best {
		best[i].Y = from.Y
	}
	return best
}

// insideMap reports whether a point is inside the mapped area, by at least the margin.
func (a *AirTraffic) insideMap(p Vec3, margin float64) bool {
	in := false
	for i, j := 0, len(a.clip)-1; i < len(a.clip); j, i = i, i+1 {
		u, v := a.clip[i], a.clip[j]
		if (u.Z > p.Z) != (v.Z > p.Z) && p.X < (v.X-u.X)*(p.Z-u.Z)/(v.Z-u.Z)+u.X {
			in = !in
		}
		edge := v.Sub(u)
		share := clamp(flat(p).Sub(u).Dot(edge)/math.Max(edge.Dot(edge), 1e-12), 0, 1)
		if flat(p).Sub(u.Add(edge.Mul(share))).Length() < margin {
			return false
		}
	}
	return in
}

func (a *AirTraffic) leg(from, to *airStrip) (*airLeg, error) {
	l := &airLeg{from: from, to: to}
	l.launch = a.ground(from.at(from.launch))
	l.touch, l.rollEnd = a.ground(to.at(to.touch)), a.ground(to.at(to.rollEnd))
	l.climbTime = (from.level - l.launch.Y) / airClimbRate
	l.climbEnd = from.at(from.launch + airLiftoffSpeed*l.climbTime)
	l.climbEnd.Y = from.level
	// The approach starts at the same level at both airports. A high cruise level would put the fix,
	// and the turn onto the approach before it, outside the mapped area west of Bråvalla.
	fixLevel := math.Min(from.level, airApproachLevel)
	l.approachTime = (fixLevel - l.touch.Y) / airSinkRate
	l.fix = to.at(to.touch - airApproachSpeed*l.approachTime)
	l.fix.Y = from.level
	// Right-hand circle through the fix: X is east and Z south, so the right of (x, z) is (-z, x).
	l.orbitCentre = l.fix.Add(Vec3{-to.dir.Z, 0, to.dir.X}.Mul(airHoldRadius))
	// Every route first, level: through its points, each passed heading from the point before it to the
	// one after.
	for _, v := range airVias[from.airport.ID] {
		points := []Vec3{l.climbEnd}
		for _, p := range v.via {
			points = append(points, Vec3{p[0], from.level, p[1]})
		}
		points = append(points, l.fix)
		r := &airRoute{name: v.name}
		heading := from.dir
		for i := 1; i < len(points); i++ {
			next := to.dir
			if i < len(points)-1 {
				next = flat(points[i+1].Sub(points[i-1])).Normalized()
			}
			part := turnStraightTurn(points[i-1], heading, points[i], next, airTurnRadius)
			if len(part) < 2 {
				return nil, fmt.Errorf("route %q from %s cannot be flown", v.name, from.airport.ID)
			}
			if i > 1 {
				part = part[1:]
			}
			r.cruise, heading = append(r.cruise, part...), next
		}
		r.cum = cumulative(r.cruise)
		l.routes = append(l.routes, r)
	}
	if len(l.routes) == 0 {
		return nil, fmt.Errorf("no route from %s", from.airport.ID)
	}
	// One cruise time for all of them: that of the first, direct route at the cruise speed, or more if
	// the longest route would otherwise have to be flown faster than airFastest.
	l.cruiseTime = l.routes[0].cum[len(l.routes[0].cum)-1] / airCruiseSpeed
	for _, r := range l.routes {
		l.cruiseTime = math.Max(l.cruiseTime, r.cum[len(r.cum)-1]/airFastest)
	}
	for _, r := range l.routes {
		samples := len(r.cruise) - 1
		level := r.cum
		// The descent to the fix, at the approach's sink rate, over the last part of the cruise.
		descent := (from.level - fixLevel) / airSinkRate * level[samples] / l.cruiseTime
		for i := range r.cruise {
			if left := level[samples] - level[i]; left < descent {
				share := left / descent
				r.cruise[i].Y = fixLevel + (from.level-fixLevel)*share*share*(3-2*share)
			}
		}
		r.cum = cumulative(r.cruise)
		r.speed = r.cum[samples] / l.cruiseTime
		if r.speed < airSlowest || r.speed > airFastest*1.01 {
			return nil, fmt.Errorf("route %q from %s would be flown at %.2f units/s", r.name, from.airport.ID, r.speed)
		}
		for _, p := range r.cruise {
			if !a.insideMap(p, airMapMargin) {
				return nil, fmt.Errorf("route %q from %s leaves the map at %.0f, %.0f", r.name, from.airport.ID, p.X, p.Z)
			}
		}
	}
	l.fix.Y, l.orbitCentre.Y = fixLevel, fixLevel
	for i := 0; i < 64; i++ {
		angle := 2 * math.Pi * float64(i) / 64
		if p := l.orbitCentre.Add(Vec3{math.Cos(angle), 0, math.Sin(angle)}.Mul(airHoldRadius)); !a.insideMap(p, airMapMargin) {
			return nil, fmt.Errorf("the holding circle for %s leaves the map", to.airport.ID)
		}
	}
	if !a.insideMap(l.climbEnd, airMapMargin) {
		return nil, fmt.Errorf("the climb from %s leaves the map", from.airport.ID)
	}
	l.flightTime = airRollTime + l.climbTime + l.cruiseTime + l.approachTime
	l.separation = to.vacate + to.lineUp + airRollTime + airFinalGap + airMargin
	return l, nil
}

// NewAirTraffic places count aircraft in the two queues, alternately, the first at the first airport.
// The seed decides the aircraft types and the route of every flight.
func NewAirTraffic(network *TransportNetwork, terrain *TerrainService, count int, seed uint64) (*AirTraffic, error) {
	a := &AirTraffic{terrain: terrain, rng: rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))}
	for _, p := range network.Meta.Clip {
		a.clip = append(a.clip, Vec3{p[0], 0, p[1]})
	}
	if len(a.clip) < 4 {
		return nil, fmt.Errorf("the network does not say where the map ends")
	}
	longest := 0.0
	for _, k := range airKinds {
		longest = math.Max(longest, k.Length)
	}
	if longest/25+.4 > airQueueGap {
		return nil, fmt.Errorf("the queue is too tight for an aircraft of %.1f m", longest)
	}
	if len(network.Airports) != 2 {
		return nil, fmt.Errorf("air traffic needs two airports, found %d", len(network.Airports))
	}
	for i := range network.Airports {
		strip, err := a.strip(network, &network.Airports[i])
		if err != nil {
			return nil, err
		}
		a.strips[i] = strip
	}
	for i := range a.legs {
		leg, err := a.leg(a.strips[i], a.strips[1-i])
		if err != nil {
			return nil, err
		}
		a.legs[i] = leg
	}
	a.timetable()
	a.SetCount(count)
	return a, nil
}

// NewBundledAirTraffic uses the bundled network and terrain, which are known to be valid, and seed 1.
func NewBundledAirTraffic(count int) *AirTraffic { return NewSeededAirTraffic(count, 1) }

// NewSeededAirTraffic is NewBundledAirTraffic with a seed of its own.
func NewSeededAirTraffic(count int, seed uint64) *AirTraffic {
	a, err := NewAirTraffic(BundledNetwork(), BundledTerrain(), count, seed)
	if err != nil {
		panic(err)
	}
	return a
}

// SetCount changes the number of aircraft. The highest numbers leave at once, wherever they are; new
// aircraft appear at the back of the shorter queue as soon as there is room behind it.
func (a *AirTraffic) SetCount(count int) {
	a.want = clampInt(count, 0, MaxAircraft)
	for len(a.planes) > a.want {
		p := a.planes[len(a.planes)-1]
		a.planes = a.planes[:len(a.planes)-1]
		for _, s := range a.strips {
			if s.runway == p {
				s.runway = nil
			}
			s.release(p)
			s.leaveHold(p)
		}
	}
	a.spawn()
}

func (s *airStrip) release(p *aircraft) {
	kept := s.inbound[:0]
	for _, w := range s.inbound {
		if w.plane != p {
			kept = append(kept, w)
		}
	}
	s.inbound = kept
}

func (s *airStrip) leaveHold(p *aircraft) {
	kept := s.holding[:0]
	for _, h := range s.holding {
		if h != p {
			kept = append(kept, h)
		}
	}
	s.holding = kept
}

// based counts the aircraft on the ground at strip i or on their way to it.
func (a *AirTraffic) based(i int) int {
	n := 0
	for _, p := range a.planes {
		if (p.state == airGround) == (p.strip == i) {
			n++
		}
	}
	return n
}

func (a *AirTraffic) spawn() {
	for len(a.planes) < a.want {
		i := 0
		if a.based(1) < a.based(0) {
			i = 1
		}
		s := a.strips[i]
		place := s.sHold
		for _, p := range a.planes {
			if p.state == airGround && p.strip == i {
				place = math.Min(place, p.s-airQueueGap)
			}
		}
		if place < s.sClear+airQueueGap {
			return // an arrival is still on its way round; try again next tick
		}
		p := &aircraft{id: fmt.Sprintf("NK-%02d", len(a.planes)+1), kind: a.rng.IntN(len(airKinds)), strip: i, s: place,
			arrived: true, ready: a.clock + airFirstReady}
		pos, tangent := along(s.path, s.cum, place)
		p.heading, p.last = math.Atan2(tangent.X, tangent.Z), a.ground(pos)
		a.planes = append(a.planes, p)
		a.describe(p, 0)
	}
}

// timetable sets the departure slots. With every aircraft leaving as soon as it may, the two runways
// fall out of step: arrivals come just too close together for another flight between them, and a busy
// fleet completes fewer flights than a smaller one. So departures leave in slots: one per period from each
// airport, placed so that the flight it starts touches down just as the other runway has let one arrival
// vacate and one departure go, and so on round. The period is the time of that round trip divided by the
// whole number of flights that fit in it (rounds).
//
// A fleet of at most that many aircraft cannot fill the runways, and leaves as soon as it may: waiting for
// a slot would only cost it flights (measured: docs/air-traffic in the UI repository).
func (a *AirTraffic) timetable() {
	wait := func(s *airStrip) float64 { return s.vacate + airSlotWindow } // from a touchdown to the next slot
	round := a.strips[0].lineUp + a.legs[0].flightTime + wait(a.strips[1]) + a.strips[1].lineUp + a.legs[1].flightTime + wait(a.strips[0])
	least := 0.0 // one arrival and one departure on a runway, both possibly late by the slot window
	for _, s := range a.strips {
		least = math.Max(least, wait(s)+airSlotWindow+s.lineUp+airRollTime+airFinalGap+airMargin)
	}
	a.rounds = int(math.Max(1, math.Floor(round/least)))
	a.period = round / float64(a.rounds)
	a.strips[0].slot = airFirstReady
	a.strips[1].slot = math.Mod(airFirstReady+a.strips[0].lineUp+a.legs[0].flightTime+wait(a.strips[1]), a.period)
}

// clear gives the first aircraft in the queue the runway if its whole flight fits.
func (a *AirTraffic) clear(p *aircraft) bool {
	here, there, leg := a.strips[p.strip], a.strips[1-p.strip], a.legs[p.strip]
	if here.runway != nil || len(here.holding) > 0 || len(there.holding) > 0 {
		return false
	}
	if late := math.Mod(a.clock-here.slot, a.period); len(a.planes) > a.rounds && (a.clock < here.slot || late > airSlotWindow) {
		return false
	}
	airborne := a.clock + here.lineUp + airRollTime
	for _, w := range here.inbound {
		if w.touch-airborne < airFinalGap+airMargin {
			return false
		}
	}
	touch := airborne + leg.flightTime - airRollTime
	for _, w := range there.inbound {
		if math.Abs(touch-w.touch) < leg.separation {
			return false
		}
	}
	p.route = a.rng.IntN(len(leg.routes))
	here.runway, p.cleared, p.freeAt, p.slot = p, true, airborne+airMargin, &airSlot{p, touch}
	there.inbound = append(there.inbound, p.slot)
	return true
}

func angleBetween(from, to float64) float64 {
	return math.Atan2(math.Sin(to-from), math.Cos(to-from))
}

// mayApproach decides at the fix (or after a holding lap) whether the runway will be free in time, and
// records the touchdown time that follows from the answer.
func (a *AirTraffic) mayApproach(p *aircraft) bool {
	leg := a.legs[p.strip]
	touch := a.clock - p.t + leg.approachTime
	free := leg.to.runway == nil || leg.to.runway.freeAt <= touch-airFinalGap
	for _, w := range leg.to.inbound {
		if w.plane != p && w.plane.state == airApproach && w.touch+leg.to.vacate > touch-airFinalGap {
			free = false
		}
	}
	if !free {
		touch += 2 * math.Pi * airHoldRadius / airCruiseSpeed
	}
	p.slot.touch = touch
	return free
}

// taxi moves the aircraft on the ground at strip i, the one nearest the runway first.
func (a *AirTraffic) taxi(i int, dt float64) {
	s := a.strips[i]
	var queue []*aircraft
	for _, p := range a.planes {
		if p.state == airGround && p.strip == i {
			queue = append(queue, p)
		}
	}
	sort.SliceStable(queue, func(x, y int) bool { return queue[x].s > queue[y].s })
	for n, p := range queue {
		if p.stepped {
			continue
		}
		if !p.cleared && p.arrived && a.clock >= p.ready && p.s >= s.sHold-1e-6 {
			a.clear(p)
		}
		limit := s.sHold
		if p.cleared {
			limit = s.sEnd
		}
		if n > 0 {
			limit = math.Min(limit, queue[n-1].s-airQueueGap)
		}
		_, tangent := along(s.path, s.cum, p.s)
		turn := angleBetween(p.heading, math.Atan2(tangent.X, tangent.Z))
		if math.Abs(turn) > airPivotAngle {
			p.heading += math.Copysign(math.Min(math.Abs(turn), airPivotRate*dt), turn)
		} else {
			p.heading += clamp(turn, -airTurnRate*dt, airTurnRate*dt)
			speed := airTaxiSpeed
			if p.s < s.sExit {
				speed = airBacktrackSpeed
			}
			p.s = math.Max(p.s, math.Min(limit, p.s+speed*dt))
		}
		if s.runway == p && !p.cleared && p.s >= s.sClear {
			s.runway = nil
			s.release(p)
			p.slot = nil
		}
		if !p.arrived && p.s > s.sClear && p.s >= limit-1e-6 {
			p.arrived, p.ready = true, a.clock+airTurnaround
		}
		if p.cleared && p.s >= s.sEnd-1e-9 {
			p.state, p.t = airRoll, 0
		}
	}
}

// fly advances an aircraft that is rolling or in the air and reports whether it touched down.
func (a *AirTraffic) fly(p *aircraft, dt float64) bool {
	leg := a.legs[p.strip]
	p.t += dt
	landed := false
	for {
		switch {
		case p.state == airRoll && p.t >= airRollTime:
			p.t -= airRollTime
			p.state = airClimb
			leg.from.runway = nil
		case p.state == airClimb && p.t >= leg.climbTime:
			p.t -= leg.climbTime
			p.state = airCruise
		case p.state == airCruise && p.t >= leg.cruiseTime:
			p.t -= leg.cruiseTime
			if len(leg.to.holding) == 0 && a.mayApproach(p) {
				p.state = airApproach
			} else {
				leg.to.holding, p.state = append(leg.to.holding, p), airOrbit
			}
		case p.state == airOrbit && p.t >= 2*math.Pi*airHoldRadius/airCruiseSpeed:
			p.t -= 2 * math.Pi * airHoldRadius / airCruiseSpeed
			if leg.to.holding[0] == p && a.mayApproach(p) {
				leg.to.leaveHold(p)
				p.state = airApproach
			}
		case p.state == airApproach && p.t >= leg.approachTime:
			p.t -= leg.approachTime
			p.state = airRollout
			if leg.to.runway != nil {
				leg.to.conflicts++
			}
			leg.to.runway, p.freeAt = p, a.clock+leg.to.vacate
			a.Completed++
			landed = true
		case p.state == airRollout && p.t >= airRolloutTime:
			p.state, p.strip, p.s, p.t = airGround, 1-p.strip, 0, 0
			p.arrived, p.cleared, p.heading = false, false, leg.to.heading
			return landed
		default:
			return landed
		}
	}
}

// position is where an aircraft in a flight phase is t seconds into it.
func (a *AirTraffic) position(p *aircraft, t float64) Vec3 {
	leg := a.legs[p.strip]
	switch p.state {
	case airRoll:
		t = clamp(t/airRollTime, 0, 1)
		return a.ground(leg.from.at(leg.from.launch * t * t))
	case airClimb:
		t = clamp(t, 0, leg.climbTime)
		at, share := leg.from.at(leg.from.launch+airLiftoffSpeed*t), t/leg.climbTime
		at.Y = leg.launch.Y + (leg.from.level-leg.launch.Y)*share*share*(3-2*share)
		return at
	case airCruise:
		route := leg.routes[p.route]
		at, _ := along(route.cruise, route.cum, t*route.speed)
		return at
	case airOrbit:
		angle := t * airCruiseSpeed / airHoldRadius
		out := leg.fix.Sub(leg.orbitCentre)
		return leg.orbitCentre.Add(out.Mul(math.Cos(angle))).Add(leg.to.dir.Mul(airHoldRadius * math.Sin(angle)))
	case airApproach:
		return leg.fix.Add(leg.touch.Sub(leg.fix).Mul(clamp(t/leg.approachTime, 0, 1)))
	default: // airRollout
		t = clamp(t/airRolloutTime, 0, 1)
		return a.ground(leg.to.at(leg.to.touch + (leg.to.rollEnd-leg.to.touch)*(1-(1-t)*(1-t))))
	}
}

// describe writes the aircraft's view after it moved by dt.
func (a *AirTraffic) describe(p *aircraft, dt float64) {
	here, there := a.strips[p.strip], a.strips[1-p.strip]
	previous := p.heading
	var pos Vec3
	pitch, phase := 0.0, ""
	origin, destination := here, there
	switch p.state {
	case airGround:
		at, _ := along(here.path, here.cum, p.s)
		pos = a.ground(at)
		switch {
		case p.cleared:
			phase = "TAXI_OUT"
		case !p.arrived:
			phase, origin, destination = "TAXI_IN", there, here
		case a.clock < p.ready:
			phase = "PARKED"
		default:
			phase = "QUEUE"
		}
	case airRoll:
		pos, phase = a.position(p, p.t), "TAKEOFF"
		p.heading += clamp(angleBetween(p.heading, here.heading), -airTurnRate*dt, airTurnRate*dt)
		if p.t > airRollTime*.88 {
			pitch = .08
		}
	case airRollout:
		pos, phase = a.position(p, p.t), "LANDING"
		p.heading = there.heading
	default:
		pos = a.position(p, p.t)
		tangent := a.position(p, p.t+.05).Sub(a.position(p, math.Max(0, p.t-.05)))
		if tangent.Length() > 1e-6 {
			p.heading = math.Atan2(tangent.X, tangent.Z)
			pitch = math.Atan2(tangent.Y, math.Hypot(tangent.X, tangent.Z))
		}
		phase = map[int]string{airClimb: "CLIMB", airCruise: "CRUISE", airOrbit: "HOLDING", airApproach: "APPROACH"}[p.state]
	}
	bank := 0.0
	if p.state == airClimb || p.state == airCruise || p.state == airOrbit || p.state == airApproach {
		bank = clamp(-angleBetween(previous, p.heading)/math.Max(dt, .001)*1.2, -.35, .35)
	}
	route := ""
	if p.cleared || p.state != airGround {
		route = a.legs[p.strip].routes[p.route].name
	}
	p.view = AircraftView{ID: p.id, Type: airKinds[p.kind].ID, Route: route,
		Pos: pos.Array(), Heading: p.heading, Pitch: pitch, Bank: bank, Phase: phase,
		Origin: origin.airport.Name, Destination: destination.airport.Name,
		Speed: pos.Sub(p.last).Length() / math.Max(dt, .001) * 25 * 3.6,
		Gear:  p.state != airClimb && p.state != airCruise && p.state != airOrbit}
	p.last = pos
}

// Tick advances all aircraft and returns those that touched down during it.
func (a *AirTraffic) Tick(dt float64) []AircraftView {
	a.clock += dt
	a.spawn()
	var touched []*aircraft
	for _, p := range a.planes {
		p.stepped = p.state != airGround
		if p.stepped && a.fly(p, dt) {
			touched = append(touched, p)
		}
	}
	a.taxi(0, dt)
	a.taxi(1, dt)
	var landed []AircraftView
	for _, p := range a.planes {
		a.describe(p, dt)
	}
	for _, p := range touched {
		landed = append(landed, p.view)
	}
	return landed
}

// Views are the aircraft as of the last tick, NK-01 first.
func (a *AirTraffic) Views() []AircraftView {
	views := make([]AircraftView, len(a.planes))
	for i, p := range a.planes {
		views[i] = p.view
	}
	return views
}
