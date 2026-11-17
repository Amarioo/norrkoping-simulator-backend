package sim

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// testdata/golden.json holds reference traces written by the Kotlin implementation
// (src/test/kotlin/.../GoldenDumpTest.kt). The Go port must reproduce them: the same random numbers,
// the same vehicle trajectories, flights, drone missions and messages.

var golden = sync.OnceValue(func() map[string]any {
	raw, err := os.ReadFile("../../testdata/golden.json")
	if err != nil {
		panic(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		panic(err)
	}
	return data
})

// asJSON turns a Go value into the generic form that golden.json is decoded to.
func asJSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	return generic
}

// diff lists where got differs from want. Numbers may differ by rounding in the last digits only:
// the JVM and Go round a few math functions (atan2, hypot, pow) differently in the last bit.
func diff(path string, want, got any, out *[]string) {
	if len(*out) >= 12 {
		return
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: want object, got %T", path, got))
			return
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			wv, inWant := w[k]
			gv, inGot := g[k]
			if !inWant || !inGot {
				*out = append(*out, fmt.Sprintf("%s.%s: present in want %v, in got %v", path, k, inWant, inGot))
				continue
			}
			diff(path+"."+k, wv, gv, out)
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			*out = append(*out, fmt.Sprintf("%s: want %d items, got %v", path, len(w), describeLen(got)))
			return
		}
		for i := range w {
			diff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], out)
		}
	case float64:
		g, ok := got.(float64)
		if !ok || math.Abs(w-g) > 1e-9*math.Max(1, math.Max(math.Abs(w), math.Abs(g))) {
			*out = append(*out, fmt.Sprintf("%s: want %v, got %v", path, want, got))
		}
	default:
		if want != got {
			*out = append(*out, fmt.Sprintf("%s: want %v, got %v", path, want, got))
		}
	}
}

func describeLen(v any) string {
	if list, ok := v.([]any); ok {
		return fmt.Sprintf("%d items", len(list))
	}
	return fmt.Sprintf("%T", v)
}

func expectSame(t *testing.T, label string, want, got any) {
	t.Helper()
	var differences []string
	diff(label, want, asJSON(t, got), &differences)
	if len(differences) > 0 {
		t.Fatalf("differs from the Kotlin reference:\n  %s", strings.Join(differences, "\n  "))
	}
}

func TestKotlinRandomSequences(t *testing.T) {
	for seed, want := range golden()["random"].(map[string]any) {
		var s int32
		fmt.Sscan(seed, &s)
		r := NewKRandom(s)
		got := map[string]any{}
		draw := func(name string, next func() any) {
			values := make([]any, 20)
			for i := range values {
				values[i] = next()
			}
			got[name] = values
		}
		draw("ints", func() any { return r.NextInt() })
		draw("doubles", func() any { return r.NextDouble() })
		draw("bounded7", func() any { return r.NextIntN(7) })
		draw("bounded64", func() any { return r.NextIntN(64) })
		draw("bounded23", func() any { return r.NextIntN(23) })
		draw("ranged", func() any { return r.NextDoubleRange(-1.2, 1.2) })
		var differences []string
		diff("random."+seed, want, asJSON(t, got), &differences)
		// Random numbers must be identical, not merely close.
		for name, values := range want.(map[string]any) {
			for i, v := range values.([]any) {
				if g := asJSON(t, got[name]).([]any)[i]; g != v {
					differences = append(differences, fmt.Sprintf("%s[%d]: want %v, got %v", name, i, v, g))
				}
			}
		}
		if len(differences) > 0 {
			t.Fatalf("seed %s:\n  %s", seed, strings.Join(differences[:min(len(differences), 12)], "\n  "))
		}
	}
}

func TestCityMatchesKotlinReference(t *testing.T) {
	want := golden()["city"].(map[string]any)
	city := NewCityService()
	if err := city.Execute(ConfigureCity{TrafficSettings{Compact: 12, Estate: 6, Trucks: 4, Flights: true, TimeScale: 20.0, Buses: 5, Trams: 3, Trains: 2}}); err != nil {
		t.Fatal(err)
	}
	marks := map[int]any{}
	for _, mark := range want["run"].([]any) {
		m := mark.(map[string]any)
		marks[int(m["step"].(float64))] = m["snapshot"]
	}
	for i := 1; i <= 3000; i++ {
		city.Step(.1)
		if snapshot, marked := marks[i]; marked {
			expectSame(t, fmt.Sprintf("step%d", i), snapshot, city.World.Value())
		}
	}
	if err := city.Execute(PauseCity{true}); err != nil {
		t.Fatal(err)
	}
	city.Step(.1)
	expectSame(t, "paused", want["paused"], city.World.Value())
	if err := city.Execute(ResetCity{}); err != nil {
		t.Fatal(err)
	}
	if err := city.Execute(ConfigureCity{TrafficSettings{Compact: 3, Estate: 2, Trucks: 1, TimeScale: 1.0, Buses: 1, Trams: 1, Trains: 1}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		city.Step(.1)
	}
	expectSame(t, "afterReset", want["afterReset"], city.World.Value())
	tooMany := DefaultTrafficSettings()
	tooMany.Compact, tooMany.Estate, tooMany.Trucks = 600, 600, 150
	err := city.Execute(ConfigureCity{tooMany})
	if err == nil || !IsValidation(err) || err.Error() != want["invalid"] {
		t.Fatalf("invalid settings: want %q, got %v", want["invalid"], err)
	}
}

func TestDronesMatchKotlinReference(t *testing.T) {
	service := NewDroneService()
	frame := int64(0)
	advance := func(to int64) {
		for frame < to {
			frame++
			service.Step(0.05, frame)
		}
	}
	marks := golden()["drones"].([]any)
	next := 0
	mark := func(label string, command Command) {
		t.Helper()
		var failure any
		if command != nil {
			if err := service.Execute(command); err != nil {
				failure = err.Error()
			}
		}
		want := marks[next].(map[string]any)
		next++
		if want["label"] != label || int64(want["frame"].(float64)) != frame {
			t.Fatalf("mark %d: reference is %v at frame %v, test is %s at frame %d", next, want["label"], want["frame"], label, frame)
		}
		if want["error"] != failure {
			t.Fatalf("%s: want error %v, got %v", label, want["error"], failure)
		}
		expectSame(t, label+".world", want["world"], service.World.Value())
		expectSame(t, label+".telemetry", want["telemetry"], service.Telemetry.Value())
	}
	museum := "arbetets museum"
	stockholm := "Stockholm"
	dispatch := DispatchRequest{Count: 3, OriginHub: "HUB_VRINNEVI", WeightKg: 1.5, Destination: &museum}
	unknown := NewDispatchRequest()
	unknown.Destination = &stockholm

	mark("initial", nil)
	advance(100)
	mark("zone", AddZoneCommand{ZoneRequest{[]float64{0.0, 15.0, 0.0}, 12.0}})
	advance(120)
	mark("zone2", AddZoneCommand{ZoneRequest{[]float64{20.0, 18.0, -20.0}, 9.5}})
	advance(200)
	mark("wind", WindCommand{WindRequest{3.5, -2.25}})
	advance(300)
	mark("dispatch", DispatchCommand{dispatch})
	mark("unknown", DispatchCommand{unknown})
	advance(1000)
	mark("cruise", nil)
	mark("removeZone", RemoveZoneCommand{"Z-1"})
	advance(1500)
	mark("recall", RecallCommand{})
	advance(2400)
	mark("home", nil)
	mark("reset", ResetCommand{})
	advance(2500)
	mark("restarted", nil)
	if next != len(marks) {
		t.Fatalf("reference has %d marks, test used %d", len(marks), next)
	}
}

// trafficTrace replays the live road simulation with an exact trace of every edge transition
// (RoadTraffic.transitionListener; per-tick sampling can skip edges shorter than one tick's travel).
//
// The schedule mirrors CityService after Configure + Reset: a fresh seeded RoadTraffic, Configure, then
// 0.05 s ticks with time advanced before each tick. Pausing only inserts wall-clock gaps, so a browser
// run with the same settings replays the same vehicle trajectories.
type trafficTrace struct {
	n         *TransportNetwork
	access    *RoadAccess
	allowed   map[string]*EdgeSet
	terminals []bool
	// Bergsbron lookups are -1 on networks without it (synthetic fixtures).
	bridgeNorth     int
	junction        int // Bredgatan junction, north of the gångfartsområde
	northOfJunction int // next Kungsgatan node towards Skvallertorget
	corridor        map[int]bool
	gangfart        map[int64]bool

	time        float64
	transitions int
	reversals   []traceReversal
	violations  []traceViolation
	episodes    []*traceEpisode
	open        map[int]*traceEpisode
	dwells      []traceDwell
	dwelling    map[int]string
	// gangfartEntries counts entries onto the four gångfartsområde ways by direction per kind.
	gangfartEntries map[string]int
	// hiddenFromSampling counts transitions that per-tick edge sampling would not have seen.
	hiddenFromSampling int
	lastTick           map[int]int
	tickIndex          int
}

type traceReversal struct {
	vehicle          int
	kind             string
	node, way        int64
	fromEdge, toEdge int
	time             float64
	typ              string
}
type traceViolation struct {
	vehicle int
	kind    string
	edge    int
	way     int64
	time    float64
}

// traceEpisode is one northbound entry onto Bergsbron by one vehicle, resolved by that same vehicle only.
type traceEpisode struct {
	vehicle  int
	kind     string
	entered  float64
	outcome  string
	resolved *float64
	detail   string
}
type traceDwell struct {
	vehicle int
	kind    string
	stop    string
	time    float64
}

func newTrafficTrace(n *TransportNetwork, access *RoadAccess) *trafficTrace {
	node := func(id int64) int {
		for i := range n.Nodes {
			if n.Nodes[i].ID == id {
				return i
			}
		}
		return -1
	}
	edge := func(from, to, way int64) int {
		for i := range n.Edges {
			if e := &n.Edges[i]; e.Way == way && n.Nodes[e.From].ID == from && n.Nodes[e.To].ID == to {
				return i
			}
		}
		return -1
	}
	t := &trafficTrace{n: n, access: access, allowed: roadPolicies(n, access), terminals: access.TurnaroundNodes(n),
		bridgeNorth: edge(286409009, 286409016, 26154805), junction: node(1108233121), northOfJunction: node(298337070),
		corridor: map[int]bool{}, gangfart: map[int64]bool{26154806: true, 1050773509: true, 444474485: true, 1212234765: true},
		open: map[int]*traceEpisode{}, dwelling: map[int]string{}, gangfartEntries: map[string]int{}, lastTick: map[int]int{}}
	// Node chain bridge south end → Bredgatan junction (Kungsgatan).
	for _, id := range []int64{286409009, 286409016, 120452, 3286754732, 3015112537, 1108233121} {
		t.corridor[node(id)] = true
	}
	return t
}

func fmt2(v float64) string { return javaFixed(v, 2) }

func (t *trafficTrace) describeReversal(r traceReversal) string {
	return fmt.Sprintf("%s #%d reversed at node %d (way %d, edge %d -> %d) at t=%s s [%s]", r.kind, r.vehicle, r.node, r.way, r.fromEdge, r.toEdge, fmt2(r.time), r.typ)
}

func (t *trafficTrace) describeEpisode(e *traceEpisode) string {
	s := fmt.Sprintf("%s #%d entered Bergsbron northbound at t=%s s: %s", e.kind, e.vehicle, fmt2(e.entered), e.outcome)
	if e.resolved != nil {
		s += " at t=" + fmt2(*e.resolved) + " s"
	}
	if e.detail != "" {
		s += " (" + e.detail + ")"
	}
	return s
}

func (t *trafficTrace) onTransition(vehicle int, kind string, fromEdge, toEdge int) {
	n := t.n
	t.transitions++
	if last, seen := t.lastTick[vehicle]; seen && last == t.tickIndex {
		t.hiddenFromSampling++
	}
	t.lastTick[vehicle] = t.tickIndex
	a, b := &n.Edges[fromEdge], &n.Edges[toEdge]
	if !t.allowed[kind].Has(toEdge) {
		t.violations = append(t.violations, traceViolation{vehicle, kind, toEdge, b.Way, t.time})
	}
	reversed := b.From == a.To && b.To == a.From
	if reversed {
		at := a.To
		typ := "artificial"
		roadEnd := true
		for _, e := range n.Outgoing[at] {
			if n.Edges[e].To != a.From {
				roadEnd = false
			}
		}
		if roadEnd {
			typ = "mappedRoadEnd"
		} else if t.terminals[at] {
			typ = "turnaroundFacility"
		}
		t.reversals = append(t.reversals, traceReversal{vehicle, kind, n.Nodes[at].ID, a.Way, fromEdge, toEdge, t.time, typ})
	}
	if t.gangfart[b.Way] && a.Way != b.Way {
		dir := "south"
		if n.Nodes[b.To].Z < n.Nodes[b.From].Z {
			dir = "north"
		}
		t.gangfartEntries[kind+"-"+dir]++
	}
	if ep := t.open[vehicle]; ep != nil {
		resolved := true
		switch {
		case reversed:
			ep.outcome, ep.detail = "reversed", fmt.Sprintf("at node %d", n.Nodes[a.To].ID)
		case b.From == t.junction && b.To == t.northOfJunction:
			ep.outcome, ep.detail = "crossed", fmt.Sprintf("continued north on way %d", b.Way)
		// Leaving onto Bredgatan is a legitimate departure, but not a crossing towards Skvallertorget.
		case b.From == t.junction && !t.corridor[b.To]:
			ep.outcome, ep.detail = "exited at junction", fmt.Sprintf("way %d", b.Way)
		case b.From == t.junction:
			ep.outcome, ep.detail = "turned back at junction", fmt.Sprintf("way %d", b.Way)
		case !t.corridor[b.To]:
			ep.outcome, ep.detail = "diverted before junction", fmt.Sprintf("way %d towards node %d", b.Way, n.Nodes[b.To].ID)
		default:
			resolved = false
		}
		if resolved {
			at := t.time
			ep.resolved = &at
			delete(t.open, vehicle)
		}
	}
	if toEdge == t.bridgeNorth {
		ep := &traceEpisode{vehicle: vehicle, kind: kind, entered: t.time, outcome: "open"}
		t.episodes = append(t.episodes, ep)
		t.open[vehicle] = ep
	}
}

// run replays up to `seconds` of simulated time using CityService.Step(0.1) arithmetic for
// settings.TimeScale; stops are sampled once per step (the live snapshot rate). until can end early.
func (t *trafficTrace) run(settings TrafficSettings, seconds float64, until func(*trafficTrace) bool) *trafficTrace {
	traffic := NewRoadTraffic(t.n, BundledTerrain(), t.access)
	if err := traffic.Configure(settings); err != nil {
		panic(err)
	}
	traffic.transitionListener = t.onTransition
	kinds := map[int]string{}
	ids, kindList, _ := traffic.EdgeStates()
	for i, id := range ids {
		kinds[id] = kindList[i]
	}
	for t.time < seconds {
		for remaining := .1 * settings.TimeScale; remaining > .000001; {
			tick := math.Min(.05, remaining)
			t.time += tick
			t.tickIndex++
			traffic.Tick(tick, t.time)
			remaining -= tick
		}
		for _, v := range traffic.Views() {
			if v.Stop == nil {
				delete(t.dwelling, v.ID)
			} else if t.dwelling[v.ID] != *v.Stop {
				t.dwelling[v.ID] = *v.Stop
				t.dwells = append(t.dwells, traceDwell{v.ID, kinds[v.ID], *v.Stop, t.time})
			}
		}
		if until != nil && until(t) {
			break
		}
	}
	return t
}

func isCar(kind string) bool { return kind == "compact" || kind == "estate" }

// failedEpisodes did not continue past the junction: reversed, turned back or diverted before it.
func (t *trafficTrace) failedEpisodes() (failed []*traceEpisode) {
	for _, e := range t.episodes {
		if e.outcome != "crossed" && e.outcome != "exited at junction" && e.outcome != "open" {
			failed = append(failed, e)
		}
	}
	return
}

// carPassages are car entries that left past the junction normally (north on Kungsgatan or onto Bredgatan).
func (t *trafficTrace) carPassages() (passages []*traceEpisode) {
	for _, e := range t.episodes {
		if isCar(e.kind) && (e.outcome == "crossed" || e.outcome == "exited at junction") {
			passages = append(passages, e)
		}
	}
	return
}

func (t *trafficTrace) crossings(kind string) (crossed []*traceEpisode) {
	for _, e := range t.episodes {
		if e.outcome == "crossed" && (e.kind == kind || (kind == "car" && isCar(e.kind))) {
			crossed = append(crossed, e)
		}
	}
	return
}

// Step 6.1b runtime regression. Replays the live service deterministically (seeded RoadTraffic, service
// tick arithmetic at time scale 20) with the same controlled scenario as the browser test
// tests/bridge-traffic.spec.ts, and stops as soon as every positive condition is met. Every edge
// transition is checked, and the whole trace must equal the Kotlin reference.
func TestBridgeTrafficRegressionMatchesKotlinReference(t *testing.T) {
	scenario := TrafficSettings{Compact: 150, Estate: 50, Trucks: 100, TimeScale: 20.0, Buses: 80}
	// ≥1 bus north to Skvallertorget, a bus dwell there, ≥3 normal car passages incl. ≥1 north on Kungsgatan.
	accepted := func(t *trafficTrace) bool {
		dwelled := false
		for _, d := range t.dwells {
			dwelled = dwelled || (d.stop == "Skvallertorget" && d.kind == "bus")
		}
		return len(t.crossings("bus")) > 0 && dwelled && len(t.carPassages()) >= 3 && len(t.crossings("car")) > 0
	}
	trace := newTrafficTrace(BundledNetwork(), BundledRoadAccess()).run(scenario, 1600.0, accepted)

	// Behavioural invariants over every transition of every vehicle class.
	if len(trace.violations) > 0 {
		t.Errorf("%d transitions outside the vehicle policy", len(trace.violations))
	}
	sawRoadEnd := false
	for _, r := range trace.reversals {
		sawRoadEnd = sawRoadEnd || r.typ == "mappedRoadEnd"
		if r.typ != "mappedRoadEnd" && r.typ != "turnaroundFacility" {
			t.Errorf("artificial reversal: %s", trace.describeReversal(r))
		}
	}
	if !sawRoadEnd {
		t.Error("no reversal at a mapped road end: legitimate terminal behaviour is gone")
	}
	if trace.allowed["truck"].Has(trace.bridgeNorth) {
		t.Error("trucks may enter the Bergsbron northbound trap")
	}
	for _, e := range trace.episodes {
		if e.kind == "truck" {
			t.Errorf("truck entered the Bergsbron northbound trap: %s", trace.describeEpisode(e))
		}
		if e.kind == "bus" && e.outcome != "open" && e.outcome != "crossed" {
			t.Errorf("bus entries must continue north to Skvallertorget: %s", trace.describeEpisode(e))
		}
	}
	for _, e := range trace.failedEpisodes() {
		t.Errorf("Bergsbron entry did not continue past the junction: %s", trace.describeEpisode(e))
	}
	if !accepted(trace) {
		t.Errorf("within 1600 s: bus crossings %d, car passages %d", len(trace.crossings("bus")), len(trace.carPassages()))
	} else if bus := trace.crossings("bus")[0]; *bus.resolved-bus.entered >= 60 {
		t.Errorf("slow crossing: %s", trace.describeEpisode(bus))
	}
	if trace.gangfartEntries["bus-north"] == 0 {
		t.Error("no bus entered the Kungsgatan gångfartsområde northbound")
	}

	got := map[string]any{"time": trace.time, "transitions": trace.transitions, "hiddenFromSampling": trace.hiddenFromSampling,
		"violations": len(trace.violations), "gangfartEntries": trace.gangfartEntries}
	reversals, episodes, dwells := []string{}, []string{}, []string{}
	for _, r := range trace.reversals {
		reversals = append(reversals, trace.describeReversal(r))
	}
	for _, e := range trace.episodes {
		episodes = append(episodes, trace.describeEpisode(e))
	}
	for _, d := range trace.dwells {
		dwells = append(dwells, fmt.Sprintf("%s #%d %s %s", d.kind, d.vehicle, d.stop, javaDouble(d.time)))
	}
	sizes, checksums := map[string]int{}, map[string]int64{}
	for kind, set := range trace.allowed {
		sizes[kind] = len(set.List)
		for _, e := range set.List {
			checksums[kind] = (checksums[kind]*31 + int64(e)) % 1000000007
		}
	}
	got["reversals"], got["episodes"], got["dwells"], got["allowed"], got["allowedChecksum"] = reversals, episodes, dwells, sizes, checksums
	expectSame(t, "trace", golden()["trace"], got)
}

func TestTraceDetectsAnArtificialReversalAndAttributesIt(t *testing.T) {
	// 0 -> 1 -> 2 dead branch where node 2 also has a link closed to trucks: the old filter kept
	// 1 <-> 2, so a truck had to reverse at 2. With throughRoadEdges no truck edge leads there.
	var nodes []RoadNode
	for i := 0; i <= 3; i++ {
		nodes = append(nodes, RoadNode{ID: int64(i), X: float64(i), Y: .071})
	}
	var edges []RoadEdge
	for i, p := range [][2]int{{0, 1}, {1, 0}, {1, 2}, {2, 1}, {2, 3}, {3, 2}} {
		edges = append(edges, RoadEdge{p[0], p[1], 1.0, .5, .3, i < 4, "T", int64(i), false, true})
	}
	trace := newTrafficTrace(NewTransportNetwork(nodes, edges, nil, nil, nil), NoRoadAccess)
	trace.onTransition(7, "truck", 2, 3) // 1 -> 2, then 2 -> 1: node 2 has another (closed) exit
	if r := trace.reversals[0]; r.typ != "artificial" || !strings.Contains(trace.describeReversal(r), "truck #7 reversed at node 2") {
		t.Fatalf("want an artificial reversal at node 2, got %s", trace.describeReversal(r))
	}
	// A reversal at a real road end (node 0 has no other exit) is legitimate terminal behaviour.
	trace.onTransition(7, "truck", 1, 0)
	if r := trace.reversals[len(trace.reversals)-1]; r.typ != "mappedRoadEnd" {
		t.Fatalf("want mappedRoadEnd, got %s", r.typ)
	}
	// A transition onto an edge outside the policy is attributed to vehicle, edge and way.
	trace.onTransition(8, "truck", 3, 4)
	if len(trace.violations) != 1 || trace.violations[0].vehicle != 8 || trace.violations[0].edge != 4 || trace.violations[0].way != 4 {
		t.Fatalf("want one violation by truck 8 on edge 4, got %+v", trace.violations)
	}
}
