package sim

import "math"

type car struct {
	id         int
	kind       string
	edge       int
	progress   float64
	speed      float64
	previous   int
	path       []int
	dwellUntil float64
	lastStop   int
}

type reservation struct {
	owner int
	until float64
}

// RoadTraffic moves cars, trucks and buses on the road graph. They share one occupancy model; rails have
// their own continuous paths.
type RoadTraffic struct {
	network *TransportNetwork
	terrain *TerrainService
	random  *KRandom
	cars    []*car
	nextID  int
	// reservations hold a junction for the vehicle that is crossing it.
	reservations map[int]*reservation
	// allowed eliminates routes that force a U-turn at a vehicle-policy boundary, and directed traps. Bus
	// access includes verified public-transport corridors and explicit OSM bus/psv tags; mapped turning
	// circles remain legitimate termini (see throughRoadEdges).
	allowed    map[string]*EdgeSet
	spawnEdges map[string][]int
	dirX, dirZ []float64

	// profiles caches each edge's height rule; it is fixed for an edge.
	profiles []*SegmentProfile
	// occupancy at the start of the tick: per edge a chain of cars, with their progress then.
	occHead     []int32
	occNext     []int32
	occEdge     []int32
	occProgress []float64
	occLength   []float64

	// transitionListener is a test hook: every edge change (vehicle id, kind, from edge, to edge).
	// Sampling edge states per tick can skip short edges.
	transitionListener func(id int, kind string, from, to int)
}

func NewRoadTraffic(network *TransportNetwork, terrain *TerrainService, access *RoadAccess) *RoadTraffic {
	t := &RoadTraffic{network: network, terrain: terrain, random: NewKRandom(1309), nextID: 1,
		reservations: map[int]*reservation{}, allowed: roadPolicies(network, access), spawnEdges: map[string][]int{},
		dirX: make([]float64, len(network.Edges)), dirZ: make([]float64, len(network.Edges)),
		profiles: make([]*SegmentProfile, len(network.Edges)), occHead: make([]int32, len(network.Edges))}
	for i := range network.Edges {
		e := &network.Edges[i]
		a, b := &network.Nodes[e.From], &network.Nodes[e.To]
		t.dirX[i], t.dirZ[i] = (b.X-a.X)/e.Length, (b.Z-a.Z)/e.Length
		t.occHead[i] = -1
	}
	for kind, set := range t.allowed {
		var pool []int
		for _, edge := range set.List {
			if network.Edges[edge].Length > .7 && len(network.Outgoing[network.Edges[edge].To]) > 0 {
				pool = append(pool, edge)
			}
		}
		t.spawnEdges[kind] = pool
	}
	return t
}

// NewBundledRoadTraffic uses the bundled network, terrain and road access.
func NewBundledRoadTraffic() *RoadTraffic {
	return NewRoadTraffic(BundledNetwork(), BundledTerrain(), BundledRoadAccess())
}

func carLength(kind string) float64 {
	switch kind {
	case "truck":
		return .49
	case "bus":
		return .48
	case "estate":
		return .19
	}
	return .16
}

// AllowedEdges is diagnostics for routing tests; not part of the API snapshot.
func (t *RoadTraffic) AllowedEdges(kind string) *EdgeSet { return t.allowed[kind] }

// Configure adds or removes vehicles so that each class has the requested count.
func (t *RoadTraffic) Configure(settings TrafficSettings) error {
	counts := []int{settings.Compact, settings.Estate, settings.Trucks, settings.Buses}
	for k, kind := range roadKinds {
		count, current := counts[k], 0
		for _, c := range t.cars {
			if c.kind == kind {
				current++
			}
		}
		if current > count {
			kept, seen := t.cars[:0:0], 0
			for _, c := range t.cars {
				if c.kind == kind {
					if seen++; seen > count {
						continue
					}
				}
				kept = append(kept, c)
			}
			t.cars = kept
		}
		if count <= current {
			continue
		}
		pool := t.spawnEdges[kind]
		if len(pool) == 0 {
			return invalid("No drivable roads for " + kind)
		}
		for i := 0; i < count-current; i++ {
			edge, progress := pool[t.random.NextIntN(len(pool))], 0.0
			for attempt := 0; attempt <= 2000; attempt++ {
				edge = pool[t.random.NextIntN(len(pool))]
				progress = t.random.NextDouble() * t.network.Edges[edge].Length
				free := true
				for _, c := range t.cars {
					if c.edge == edge && math.Abs(c.progress-progress) < .8 {
						free = false
						break
					}
				}
				if free {
					break
				}
			}
			t.cars = append(t.cars, &car{id: t.nextID, kind: kind, edge: edge, progress: progress, previous: edge, lastStop: -1})
			t.nextID++
		}
	}
	live := make(map[int]bool, len(t.cars))
	for _, c := range t.cars {
		live[c.id] = true
	}
	for node, r := range t.reservations {
		if !live[r.owner] {
			delete(t.reservations, node)
		}
	}
	return nil
}

func (t *RoadTraffic) fillPath(c *car) {
	edge := c.edge
	if len(c.path) > 0 {
		edge = c.path[len(c.path)-1]
	}
	allowed := t.allowed[c.kind]
	var onward []int
	var weights []float64
	for len(c.path) < 32 {
		old := &t.network.Edges[edge]
		dx, dz := t.dirX[edge], t.dirZ[edge]
		onward = onward[:0]
		choices := 0
		for _, next := range t.network.Outgoing[old.To] {
			if allowed.Has(next) {
				choices++
				if t.network.Edges[next].To != old.From {
					onward = append(onward, next)
				}
			}
		}
		if len(onward) == 0 {
			if choices == 0 {
				break
			}
			// Only the way back remains: a road end.
			for _, next := range t.network.Outgoing[old.To] {
				if allowed.Has(next) {
					onward = append(onward, next)
				}
			}
		}
		weights = weights[:0]
		sum := 0.0
		for _, next := range onward {
			w := math.Max(.08, 1.0+dx*t.dirX[next]+dz*t.dirZ[next])
			weights = append(weights, w)
			sum += w
		}
		sample := t.random.NextDouble() * sum
		chosen := onward[len(onward)-1]
		for i := range onward {
			sample -= weights[i]
			if sample <= 0 {
				chosen = onward[i]
				break
			}
		}
		c.path = append(c.path, chosen)
		edge = chosen
	}
}

func (t *RoadTraffic) green(node *RoadNode, edge int, time float64) bool {
	horizontal := math.Abs(t.dirX[edge]) > math.Abs(t.dirZ[edge])
	if node.SignalHorizontal != nil {
		horizontal = *node.SignalHorizontal
	}
	phase := math.Mod(time, 48)
	if horizontal {
		return phase < 20
	}
	return phase >= 24 && phase < 44
}

// Tick advances every vehicle by dt at simulation time `time`.
func (t *RoadTraffic) Tick(dt, time float64) {
	edges, nodes := t.network.Edges, t.network.Nodes
	for node, r := range t.reservations {
		if r.until < time {
			delete(t.reservations, node)
		}
	}
	if cap(t.occNext) < len(t.cars) {
		t.occNext = make([]int32, len(t.cars))
		t.occEdge = make([]int32, len(t.cars))
		t.occProgress = make([]float64, len(t.cars))
		t.occLength = make([]float64, len(t.cars))
	}
	t.occNext, t.occEdge = t.occNext[:len(t.cars)], t.occEdge[:len(t.cars)]
	t.occProgress, t.occLength = t.occProgress[:len(t.cars)], t.occLength[:len(t.cars)]
	for i, c := range t.cars {
		t.fillPath(c)
		t.occEdge[i] = int32(c.edge)
		t.occNext[i] = t.occHead[c.edge]
		t.occHead[c.edge] = int32(i)
		t.occProgress[i] = c.progress
		t.occLength[i] = carLength(c.kind)
	}
	var visited [34]int
	for index, c := range t.cars {
		e := &edges[c.edge]
		length := carLength(c.kind)
		if c.lastStop != e.From && c.lastStop != e.To {
			c.lastStop = -1
		}
		gap := math.Inf(1)
		for o := t.occHead[c.edge]; o >= 0; o = t.occNext[o] {
			if int(o) != index && t.occProgress[o] > c.progress {
				gap = math.Min(gap, t.occProgress[o]-c.progress-(t.occLength[o]+length)/2)
			}
		}
		remaining := e.Length - c.progress
		visited[0] = c.edge
		seen := 1
		for _, next := range c.path {
			// Never reinterpret cars behind us as obstacles ahead after a short route loop.
			if remaining > 4 || contains(visited[:seen], next) {
				break
			}
			visited[seen] = next
			seen++
			for o := t.occHead[next]; o >= 0; o = t.occNext[o] {
				if int(o) != index {
					gap = math.Min(gap, remaining+t.occProgress[o]-(t.occLength[o]+length)/2)
				}
			}
			remaining += edges[next].Length
		}
		signalDistance := e.Length - c.progress
		seen = 0
		for step := -1; step < len(c.path); step++ {
			edge := c.edge
			if step >= 0 {
				edge = c.path[step]
			}
			if signalDistance > 4 || contains(visited[:seen], edge) {
				break
			}
			visited[seen] = edge
			seen++
			n := &nodes[edges[edge].To]
			if n.Signal && !t.green(n, edge, time) {
				gap = math.Min(gap, signalDistance-length/2-.04)
				break
			}
			// The edge after this one, as the path lists it (its first occurrence).
			following := -1
			if edge == c.edge {
				if len(c.path) > 0 {
					following = c.path[0]
				}
			} else {
				for i, candidate := range c.path {
					if candidate == edge {
						if i+1 < len(c.path) {
							following = c.path[i+1]
						}
						break
					}
				}
			}
			if following >= 0 {
				signalDistance += edges[following].Length
			}
		}
		left := e.Length - c.progress
		n := &nodes[e.To]
		if t.network.Junctions[e.To] && left < math.Max(.9, c.speed*2) {
			owner := t.reservations[e.To]
			permitted := !n.Signal || t.green(n, c.edge, time)
			if owner == nil && permitted && gap > left+length/2+.12 {
				owner = &reservation{c.id, time + 8}
				t.reservations[e.To] = owner
			}
			if owner == nil || owner.owner != c.id {
				gap = math.Min(gap, left-length/2-.04)
			}
		}
		if c.kind == "bus" && n.BusStop != nil && left < .32 && c.lastStop != e.To {
			c.lastStop = e.To
			c.dwellUntil = time + 8
		}
		limit := 1.23
		switch c.kind {
		case "truck":
			limit = .89
		case "bus":
			limit = .61
		}
		limit = math.Min(e.Speed, limit)
		desired := 0.0
		if time >= c.dwellUntil {
			desired = math.Min(limit, math.Max(0.0, (gap-.10)/1.2))
		}
		c.speed += clamp(desired-c.speed, -.25*dt, .075*dt)
		distance := math.Min(c.speed*dt, math.Max(0.0, gap-.06))
		for distance > 0 {
			edge := &edges[c.edge]
			rest := edge.Length - c.progress
			if distance < rest {
				c.progress += distance
				break
			}
			if len(c.path) == 0 {
				c.progress = edge.Length
				c.speed = 0.0
				break
			}
			if r := t.reservations[edge.To]; r != nil && r.owner == c.id {
				r.until = time + (length+.1)/math.Max(.15, c.speed)
			}
			distance -= rest
			c.previous = c.edge
			c.edge = c.path[0]
			c.path = c.path[1:]
			c.progress = 0.0
			if t.transitionListener != nil {
				t.transitionListener(c.id, c.kind, c.previous, c.edge)
			}
		}
	}
	// Every car was chained on the edge it started the tick on; clear those heads again.
	for _, edge := range t.occEdge {
		t.occHead[edge] = -1
	}
}

func contains(list []int, value int) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

// Views are the vehicles as the API shows them.
func (t *RoadTraffic) Views() []VehicleView {
	edges, nodes := t.network.Edges, t.network.Nodes
	views := make([]VehicleView, len(t.cars))
	for i, c := range t.cars {
		e := &edges[c.edge]
		a, b := &nodes[e.From], &nodes[e.To]
		u := clamp(c.progress/e.Length, 0.0, 1.0)
		next := c.edge
		if len(c.path) > 0 {
			next = c.path[0]
		}
		blend := math.Min(.24, e.Length*.3)
		start := clamp(1-c.progress/blend, 0.0, 1.0) * .5
		end := clamp(1-(e.Length-c.progress)/blend, 0.0, 1.0) * .5
		dx := t.dirX[c.edge]*(1-start-end) + t.dirX[c.previous]*start + t.dirX[next]*end
		dz := t.dirZ[c.edge]*(1-start-end) + t.dirZ[c.previous]*start + t.dirZ[next]*end
		norm := math.Max(math.Hypot(dx, dz), .001)
		dx /= norm
		dz /= norm
		offset := math.Min(e.Width*.25, .075)
		if e.Oneway && e.Width < .24 {
			offset = 0.0
		}
		profile := t.profiles[c.edge]
		if profile == nil {
			profile = t.terrain.Segment([]float64{a.X, a.Y, a.Z}, []float64{b.X, b.Y, b.Z})
			t.profiles[c.edge] = profile
		}
		var stop *string
		if c.kind == "bus" && c.speed < .015 && c.lastStop >= 0 && c.lastStop < len(nodes) {
			stop = nodes[c.lastStop].BusStop
		}
		views[i] = VehicleView{ID: c.id, Kind: c.kind,
			Pos:     []float64{a.X + (b.X-a.X)*u - dz*offset, profile.Height(u) + .008, a.Z + (b.Z-a.Z)*u + dx*offset},
			Heading: math.Atan2(dx, dz), Speed: c.speed * 90, Road: e.Name, Stopped: c.speed < .015,
			Sections: []VehicleSection{}, Stop: stop}
	}
	return views
}

// EdgeStates lists (vehicle id, kind, edge) for routing tests.
func (t *RoadTraffic) EdgeStates() (ids []int, kinds []string, edges []int) {
	for _, c := range t.cars {
		ids, kinds, edges = append(ids, c.id), append(kinds, c.kind), append(edges, c.edge)
	}
	return
}
