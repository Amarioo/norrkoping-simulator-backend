package sim

import "math"

// Rail track contract, mirrored from the UI's src/lib/trackProfile.ts and verified against its fixture
// (src/test/resources/rail-track-control.json). Transit route points are the drawn track's nodes,
// carrying the node's own network reference key, so vehicles use exactly the drawn rails' height rules
// (deck profiles, engineered spans, approaches, terrain). The rail head above the drawn running surface
// is a per-kind offset; vehicles rest on their two bogies (pitch follows the track).

type railKind struct {
	// RailHeadMetres is the top of rail above the drawn running surface (ballasted mainline / embedded tram rail).
	RailHeadMetres float64
	// BogieHalfMetres are the bogie centres either side of the section centre (X61 car 19.5 m / tram module 6.12 m).
	BogieHalfMetres float64
	// SectionSpacingMetres is the section centre spacing.
	SectionSpacingMetres float64
	Sections             int
}

var RailContract = map[string]railKind{
	"train": {.18, 6.24, 20.0, 4},
	"tram":  {.0, 1.8, 6.4, 5},
}

// RailTrack is a transit route as a continuous track: rail-top position and bogie-based pose by distance
// (scene units).
type RailTrack struct {
	Route          *TransitRoute
	terrain        *TerrainService
	Lift           float64
	BogieHalf      float64
	SectionSpacing float64
	cumulative     []float64
	profiles       []*SegmentProfile
}

func NewRailTrack(route *TransitRoute, terrain *TerrainService) *RailTrack {
	unit := terrain.Data.Reference.MetresPerUnit
	contract := RailContract[route.Kind]
	// The drawn running surface above the network reference is terrain.ts ROAD_SURFACE.
	t := &RailTrack{Route: route, terrain: terrain, Lift: streetAboveRoad + contract.RailHeadMetres/unit,
		BogieHalf: contract.BogieHalfMetres / unit, SectionSpacing: contract.SectionSpacingMetres / unit,
		cumulative: make([]float64, len(route.Points)), profiles: make([]*SegmentProfile, len(route.Points))}
	for i := 1; i < len(route.Points); i++ {
		t.cumulative[i] = t.cumulative[i-1] + math.Hypot(route.Points[i][0]-route.Points[i-1][0], route.Points[i][2]-route.Points[i-1][2])
	}
	return t
}

func (t *RailTrack) Length() float64 { return t.cumulative[len(t.cumulative)-1] }

// wrap maps a distance onto [0, length).
func wrap(distance, length float64) float64 {
	return math.Mod(math.Mod(distance, length)+length, length)
}

// locate returns the segment index and parameter at a distance wrapped onto the loop (same search as
// trackProfile.ts).
func (t *RailTrack) locate(distance float64) (int, float64) {
	d := wrap(distance, t.Length())
	lo, hi := 0, len(t.cumulative)-1
	for hi-lo > 1 {
		if mid := (lo + hi) >> 1; t.cumulative[mid] <= d {
			lo = mid
		} else {
			hi = mid
		}
	}
	i := min(lo, len(t.cumulative)-2)
	return i, clamp((d-t.cumulative[i])/math.Max(t.cumulative[i+1]-t.cumulative[i], 1e-5), 0.0, 1.0)
}

// Position is the rail-top point [x, y, z] at a distance.
func (t *RailTrack) Position(distance float64) []float64 {
	i, u := t.locate(distance)
	a, b := t.Route.Points[i], t.Route.Points[i+1]
	if t.profiles[i] == nil {
		t.profiles[i] = t.terrain.Segment(a, b)
	}
	return []float64{a[0] + (b[0]-a[0])*u, t.profiles[i].Height(u) + t.Lift, a[2] + (b[2]-a[2])*u}
}

// Pose puts both bogie centres on the rail top; the body centre is on the chord between them.
func (t *RailTrack) Pose(distance float64) VehicleSection {
	f, r := t.Position(distance+t.BogieHalf), t.Position(distance-t.BogieHalf)
	dx, dz := f[0]-r[0], f[2]-r[2]
	return VehicleSection{[]float64{(f[0] + r[0]) / 2, (f[1] + r[1]) / 2, (f[2] + r[2]) / 2}, math.Atan2(dx, dz), math.Atan2(f[1]-r[1], math.Hypot(dx, dz))}
}

type railUnit struct {
	id       int
	track    *RailTrack
	distance float64
	speed    float64
	dwell    float64
	lastStop *string
}

// RailTraffic moves trams and trains. Rail vehicles follow continuous OSM polylines; each articulated
// section rests on its own two bogies.
type RailTraffic struct {
	tracks   []*RailTrack
	units    []*railUnit
	nextID   int
	previous []float64
}

func NewRailTraffic(routes []TransitRoute, terrain *TerrainService) *RailTraffic {
	t := &RailTraffic{nextID: 10001}
	for i := range routes {
		t.tracks = append(t.tracks, NewRailTrack(&routes[i], terrain))
	}
	return t
}

// NewBundledRailTraffic uses the bundled transit routes and terrain.
func NewBundledRailTraffic() *RailTraffic {
	return NewRailTraffic(BundledNetwork().TransitRoutes, BundledTerrain())
}

func railLength(u *railUnit) float64 {
	if u.track.Route.Kind == "train" {
		return 3.2
	}
	return 1.28
}

// Configure replaces the trams or trains when their count changes, spread evenly over their routes.
func (t *RailTraffic) Configure(settings TrafficSettings) error {
	for _, want := range []struct {
		kind  string
		count int
	}{{"tram", settings.Trams}, {"train", settings.Trains}} {
		kind, count := want.kind, want.count
		old := 0
		for _, u := range t.units {
			if u.track.Route.Kind == kind {
				old++
			}
		}
		if old == count {
			continue
		}
		kept := t.units[:0:0]
		for _, u := range t.units {
			if u.track.Route.Kind != kind {
				kept = append(kept, u)
			}
		}
		t.units = kept
		var available []*RailTrack
		for _, track := range t.tracks {
			if track.Route.Kind == kind {
				available = append(available, track)
			}
		}
		if count != 0 && len(available) == 0 {
			return invalid("Missing mapped " + kind + " routes")
		}
		for i := 0; i < count; i++ {
			track := available[i%len(available)]
			onTrack := (count + len(available) - 1 - i%len(available)) / len(available)
			progress := (float64(i/len(available)) + .25) / float64(onTrack) * track.Length()
			t.units = append(t.units, &railUnit{id: t.nextID, track: track, distance: progress})
			t.nextID++
		}
	}
	return nil
}

// Tick advances every rail vehicle by dt.
func (t *RailTraffic) Tick(dt float64) {
	t.previous = t.previous[:0]
	for _, u := range t.units {
		t.previous = append(t.previous, u.distance)
	}
	for index, u := range t.units {
		if u.dwell > 0 {
			u.dwell = math.Max(0.0, u.dwell-dt)
			u.speed = 0.0
			continue
		}
		track := u.track
		length := track.Length()
		gap := math.Inf(1)
		for other, o := range t.units {
			if o != u && o.track == track {
				ahead := wrap(t.previous[other]-t.previous[index], length)
				gap = math.Min(gap, ahead-railLength(o)-.25)
			}
		}
		var stop *TransitStop
		stopDistance := math.Inf(1)
		for i := range track.Route.Stops {
			s := &track.Route.Stops[i]
			if d := wrap(s.Distance-u.distance, length); d > .005 && (stop == nil || d < stopDistance) {
				stop, stopDistance = s, d
			}
		}
		limit := .50
		if track.Route.Kind == "train" {
			limit = 1.35
		}
		desired := math.Min(limit, math.Min(math.Sqrt(math.Max(0.0, 2*.045*(stopDistance-.01))), math.Max(0.0, gap/1.8)))
		u.speed += clamp(desired-u.speed, -.08*dt, .032*dt)
		distance := math.Min(u.speed*dt, math.Max(0.0, gap))
		if stop != nil && stopDistance < math.Max(.025, distance) {
			u.distance = math.Mod(stop.Distance+.006, length)
			u.dwell = 14.0
			if track.Route.Kind == "train" {
				u.dwell = 35.0
			}
			u.lastStop = &stop.Name
			u.speed = 0.0
		} else {
			u.distance = math.Mod(u.distance+distance, length)
		}
	}
}

// Views are the rail vehicles as the API shows them, front section first.
func (t *RailTraffic) Views() []VehicleView {
	views := make([]VehicleView, len(t.units))
	for i, u := range t.units {
		track := u.track
		sections := make([]VehicleSection, RailContract[track.Route.Kind].Sections)
		for k := range sections {
			sections[k] = track.Pose(u.distance - float64(k)*track.SectionSpacing)
		}
		front := sections[0]
		var stop, destination *string
		if u.dwell > 0 {
			stop = u.lastStop
		}
		for k := range track.Route.Destinations {
			if d := &track.Route.Destinations[k]; d.Distance <= u.distance {
				destination = &d.Name
			}
		}
		views[i] = VehicleView{u.id, track.Route.Kind, front.Pos, front.Heading, u.speed * 90, track.Route.Name, u.speed < .015, sections, stop, destination, front.Pitch}
	}
	return views
}
