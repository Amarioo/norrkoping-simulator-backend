package sim

import (
	"fmt"
	"math"
)

// runwaySurface: ground positions are 0.01 above the runway pavement, which was the legacy level 0.09
// (mapLandmarks.ts).
const runwaySurface = .09

type airfield struct {
	airport   *Airport
	start     Vec3
	end       Vec3
	direction Vec3
	gate      Vec3
	taxiOut   []Vec3
	taxiIn    []Vec3
	exit      Vec3
}

// RegionalFlight is a fictional shuttle using mapped runway headings and taxiway polylines.
type RegionalFlight struct {
	network         *TransportNetwork
	terrain         *TerrainService
	fields          []*airfield
	origin          int
	phase           string
	elapsed         float64
	Completed       int
	lastPosition    Vec3
	previousHeading float64
	view            AircraftView
}

func (f *RegionalFlight) point(p []float64) Vec3 {
	return Vec3{p[0], f.terrain.PlaceLegacy(p[0], p[1], .1, runwaySurface), p[1]}
}

func distinct(points []Vec3) []Vec3 {
	seen := make(map[vecKey]bool, len(points))
	unique := points[:0:0]
	for _, p := range points {
		if !seen[p.key()] {
			seen[p.key()] = true
			unique = append(unique, p)
		}
	}
	return unique
}

// firstMin returns the first candidate with the smallest score, as Kotlin's minBy does.
func firstMin(candidates []int, score func(int) float64) int {
	best, bestScore := candidates[0], score(candidates[0])
	for _, c := range candidates[1:] {
		if s := score(c); s < bestScore {
			best, bestScore = c, s
		}
	}
	return best
}

func (f *RegionalFlight) airfield(a *Airport) (*airfield, error) {
	start, end := f.point(a.Start), f.point(a.End)
	direction := end.Sub(start).Normalized()
	runwayLength := end.Sub(start).Length()
	var ways []*Airway
	var all []Vec3
	for i := range f.network.Airways {
		w := &f.network.Airways[i]
		if w.Kind == "taxiway" && f.point(w.Points[0]).Sub(start).Length() < 100 {
			ways = append(ways, w)
			for _, p := range w.Points {
				all = append(all, f.point(p))
			}
		}
	}
	points := distinct(all)
	if len(points) == 0 {
		return nil, fmt.Errorf("no taxiways at %s", a.ID)
	}
	index := make(map[vecKey]int, len(points))
	for i, p := range points {
		index[p.key()] = i
	}
	type link struct {
		to       int
		distance float64
	}
	links := make([][]link, len(points))
	for _, w := range ways {
		for i := 1; i < len(w.Points); i++ {
			x, y := index[f.point(w.Points[i-1]).key()], index[f.point(w.Points[i]).key()]
			d := points[x].Sub(points[y]).Length()
			links[x] = append(links[x], link{y, d})
			links[y] = append(links[y], link{x, d})
		}
	}
	// The largest connected taxiway system, its nodes in breadth-first order.
	unseen := make([]bool, len(points))
	for i := range unseen {
		unseen[i] = true
	}
	var connected []int
	for first := 0; first < len(points); first++ {
		if !unseen[first] {
			continue
		}
		var component []int
		queue := []int{first}
		for head := 0; head < len(queue); head++ {
			n := queue[head]
			if !unseen[n] {
				continue
			}
			unseen[n] = false
			component = append(component, n)
			for _, l := range links[n] {
				queue = append(queue, l.to)
			}
		}
		if len(component) > len(connected) {
			connected = component
		}
	}
	nearest := func(p Vec3) int {
		return firstMin(connected, func(i int) float64 { return points[i].Sub(p).Length() })
	}
	mid := start.Add(end).Mul(.5)
	gate := firstMin(connected, func(i int) float64 {
		p := points[i]
		rel := p.Sub(start)
		return math.Abs(p.Sub(mid).Dot(direction)) + math.Abs(math.Abs(rel.X*direction.Z-rel.Z*direction.X)-2.5)*2
	})
	threshold := nearest(start)
	along := func(i int) float64 { return points[i].Sub(start).Dot(direction) / runwayLength }
	var exits []int
	for _, i := range connected {
		rel := points[i].Sub(start)
		if t := along(i); t >= .45 && t <= 0.95 && math.Abs(rel.X*direction.Z-rel.Z*direction.X) < a.Width*.55 {
			exits = append(exits, i)
		}
	}
	var exitIndex int
	if len(exits) > 0 {
		exitIndex = firstMin(exits, func(i int) float64 { return math.Abs(along(i) - .65) })
	} else {
		exitIndex = nearest(start.Add(end.Sub(start).Mul(.65)))
	}
	exit := start.Add(direction.Mul(clamp(points[exitIndex].Sub(start).Dot(direction), runwayLength*.45, runwayLength*.95)))
	route := func(from, to int) ([]Vec3, error) {
		distance := make([]float64, len(points))
		previous := make([]int, len(points))
		for i := range distance {
			distance[i], previous[i] = math.Inf(1), -1
		}
		type entry struct {
			node int
			cost float64
		}
		queue := javaPQ[entry]{less: func(a, b entry) bool { return a.cost < b.cost }}
		distance[from] = 0.0
		queue.Add(entry{from, 0.0})
		for queue.Len() > 0 {
			e := queue.Remove()
			if e.cost > distance[e.node] {
				continue
			}
			if e.node == to {
				break
			}
			for _, l := range links[e.node] {
				if e.cost+l.distance < distance[l.to] {
					distance[l.to] = e.cost + l.distance
					previous[l.to] = e.node
					queue.Add(entry{l.to, distance[l.to]})
				}
			}
		}
		if !isFinite(distance[to]) {
			return nil, fmt.Errorf("disconnected taxiway at %s", a.ID)
		}
		var path []Vec3
		for node := to; node != -1; node = previous[node] {
			path = append(path, points[node])
			if node == from {
				break
			}
		}
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		return path, nil
	}
	out, err := route(gate, threshold)
	if err != nil {
		return nil, err
	}
	in, err := route(exitIndex, gate)
	if err != nil {
		return nil, err
	}
	return &airfield{a, start, end, direction, points[gate], distinct(append(out, start)), distinct(append([]Vec3{exit}, in...)), exit}, nil
}

// NewRegionalFlight parks the aircraft at the first airport's gate. The network needs two airports.
func NewRegionalFlight(network *TransportNetwork, terrain *TerrainService) (*RegionalFlight, error) {
	f := &RegionalFlight{network: network, terrain: terrain, phase: "PARKED"}
	if len(network.Airports) < 2 {
		return nil, fmt.Errorf("the regional flight needs two airports, found %d", len(network.Airports))
	}
	for i := range network.Airports {
		field, err := f.airfield(&network.Airports[i])
		if err != nil {
			return nil, err
		}
		f.fields = append(f.fields, field)
	}
	f.lastPosition = f.fields[0].gate
	f.previousHeading = math.Atan2(f.fields[0].direction.X, f.fields[0].direction.Z)
	f.view = AircraftView{ID: "NK-01", Pos: f.lastPosition.Array(), Heading: f.previousHeading, Phase: f.phase,
		Origin: f.fields[0].airport.Name, Destination: f.fields[1].airport.Name, Gear: true}
	return f, nil
}

// NewBundledRegionalFlight uses the bundled network and terrain, which are known to be valid.
func NewBundledRegionalFlight() *RegionalFlight {
	f, err := NewRegionalFlight(BundledNetwork(), BundledTerrain())
	if err != nil {
		panic(err)
	}
	return f
}

func pathLength(path []Vec3) float64 {
	sum := 0.0
	for i := 1; i < len(path); i++ {
		sum += path[i].Sub(path[i-1]).Length()
	}
	return sum
}

func pathAlong(path []Vec3, t float64) Vec3 {
	dist := pathLength(path) * clamp(t, 0.0, 1.0)
	for i := 1; i < len(path); i++ {
		a, b := path[i-1], path[i]
		l := b.Sub(a).Length()
		if dist <= l {
			return a.Add(b.Sub(a).Mul(dist / math.Max(l, .0001)))
		}
		dist -= l
	}
	return path[len(path)-1]
}

func bezier(a, b, c, d Vec3, t float64) Vec3 {
	return a.Mul(math.Pow(1-t, 3)).Add(b.Mul(3 * math.Pow(1-t, 2) * t)).Add(c.Mul(3 * (1 - t) * t * t)).Add(d.Mul(t * t * t))
}

func (f *RegionalFlight) endpoints() (Vec3, Vec3) {
	from, to := f.fields[f.origin], f.fields[1-f.origin]
	a := from.end.Add(from.direction.Mul(from.end.Sub(from.start).Length() * .2))
	b := to.start.Sub(to.direction.Mul(85.0))
	return Vec3{a.X, 8.0, a.Z}, Vec3{b.X, 8.0, b.Z}
}

func (f *RegionalFlight) duration() float64 {
	from, to := f.fields[f.origin], f.fields[1-f.origin]
	d := 1.0
	switch f.phase {
	case "PARKED":
		d = 10.0
	case "TAXI_OUT":
		d = pathLength(from.taxiOut) / .18
	case "TAKEOFF":
		d = 35.0
	case "CLIMB":
		d = 25.0
	case "CRUISE":
		a, b := f.endpoints()
		d = math.Max(35.0, b.Sub(a).Length()/2.8)
	case "APPROACH":
		d = 38.0
	case "LANDING":
		d = 20.0
	case "TAXI_IN":
		d = pathLength(to.taxiIn) / .18
	}
	return math.Max(d, .1)
}

func (f *RegionalFlight) position(t float64) Vec3 {
	from, to := f.fields[f.origin], f.fields[1-f.origin]
	launch := from.start.Add(from.end.Sub(from.start).Mul(.65))
	a, b := f.endpoints()
	touch := to.start.Add(to.end.Sub(to.start).Mul(.12))
	switch f.phase {
	case "TAXI_OUT":
		return pathAlong(from.taxiOut, t)
	case "TAKEOFF":
		p := from.start.Add(launch.Sub(from.start).Mul(t * t))
		return Vec3{p.X, p.Y + math.Pow(math.Max(0.0, (t-.78)/.22), 2)*1.3, p.Z}
	case "CLIMB":
		lifted := Vec3{launch.X, launch.Y + 1.3, launch.Z}
		return lifted.Add(a.Sub(lifted).Mul(t))
	case "CRUISE":
		return bezier(a, a.Add(from.direction.Mul(65.0)), b.Sub(to.direction.Mul(65.0)), b, t)
	case "APPROACH":
		return b.Add(touch.Sub(b).Mul(t))
	case "LANDING":
		return touch.Add(to.exit.Sub(touch).Mul(1 - math.Pow(1-t, 2)))
	case "TAXI_IN":
		return pathAlong(to.taxiIn, t)
	}
	return from.gate
}

// Tick advances the flight and reports whether it touched down during this tick.
func (f *RegionalFlight) Tick(dt float64) bool {
	f.elapsed += dt
	landed := false
	for f.elapsed >= f.duration() {
		f.elapsed -= f.duration()
		switch f.phase {
		case "PARKED":
			f.phase = "TAXI_OUT"
		case "TAXI_OUT":
			f.phase = "TAKEOFF"
		case "TAKEOFF":
			f.phase = "CLIMB"
		case "CLIMB":
			f.phase = "CRUISE"
		case "CRUISE":
			f.phase = "APPROACH"
		case "APPROACH":
			f.Completed++
			landed = true
			f.phase = "LANDING"
		case "LANDING":
			f.phase = "TAXI_IN"
		default:
			f.origin = 1 - f.origin
			f.phase = "PARKED"
		}
	}
	t := clamp(f.elapsed/f.duration(), 0.0, 1.0)
	p := f.position(t)
	tangent := f.position(math.Min(1.0, t+.002)).Sub(f.position(math.Max(0.0, t-.002)))
	heading := f.previousHeading
	if tangent.Length() > .0001 {
		heading = math.Atan2(tangent.X, tangent.Z)
	}
	turn := math.Atan2(math.Sin(heading-f.previousHeading), math.Cos(heading-f.previousHeading))
	airborne := f.phase == "CLIMB" || f.phase == "CRUISE" || f.phase == "APPROACH"
	pitch, bank := 0.0, 0.0
	if airborne {
		pitch = math.Atan2(tangent.Y, math.Hypot(tangent.X, tangent.Z))
		bank = clamp(-turn/math.Max(dt, .001)*1.2, -.35, .35)
	} else if f.phase == "TAKEOFF" && t > .78 {
		pitch = .08
	}
	f.view = AircraftView{ID: "NK-01", Pos: p.Array(), Heading: heading, Pitch: pitch, Bank: bank, Phase: f.phase,
		Origin: f.fields[f.origin].airport.Name, Destination: f.fields[1-f.origin].airport.Name,
		Speed: p.Sub(f.lastPosition).Length() / math.Max(dt, .001) * 25 * 3.6, Gear: f.phase != "CLIMB" && f.phase != "CRUISE"}
	f.previousHeading = heading
	f.lastPosition = p
	return landed
}

// View is the aircraft as of the last tick.
func (f *RegionalFlight) View() AircraftView { return f.view }
