package sim

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// These tests are the Kotlin test suite, ported. Fixtures stay in src/test/resources.

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../src/test/resources/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func numbers(row any) []float64 {
	list := row.([]any)
	values := make([]float64, len(list))
	for i, v := range list {
		values[i] = v.(float64)
	}
	return values
}

func near(t *testing.T, label string, want, got, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(want-got) > tolerance {
		t.Fatalf("%s: want %v, got %v (tolerance %v)", label, want, got, tolerance)
	}
}

func pointer[T any](v T) *T { return &v }

// --- Pathfinder and drones (SimulationTest.kt) ---

func TestPathRoutesAroundASphereWithClearanceOnEverySegment(t *testing.T) {
	start, end := Vec3{-40.0, 18.0, 0.0}, Vec3{40.0, 18.0, 0.0}
	zones := []ZoneView{{"Z-1", []float64{0.0, 15.0, 0.0}, 12.0}}
	if PathClear(start, end, zones) {
		t.Fatal("the direct line should be blocked")
	}
	route := PathRoute(start, end, zones)
	if len(route) < 2 || route[len(route)-1] != end {
		t.Fatalf("route %v", route)
	}
	previous := start
	for _, p := range route {
		if !PathClear(previous, p, zones) {
			t.Fatalf("segment %v-%v enters the zone", previous, p)
		}
		previous = p
	}
	if blocked := PathRoute(Vec3{-40.0, 18.0, 0.0}, Vec3{0.0, 18.0, 0.0}, zones); len(blocked) != 0 {
		t.Fatalf("a blocked destination has no route, got %v", blocked)
	}
}

func TestFleetServesTheNorrkopingCityMap(t *testing.T) {
	service := NewDroneService()
	world := service.World.Value()
	if world.Hubs[0].ID != "HUB_HAMNEN" || world.Hubs[1].ID != "HUB_VRINNEVI" {
		t.Fatalf("hubs %v", world.Hubs)
	}
	request := NewDispatchRequest()
	request.Destination = pointer("arbetets museum")
	if err := service.Execute(DispatchCommand{request}); err != nil {
		t.Fatal(err)
	}
	drones := service.World.Value().Drones
	last := drones[len(drones)-1]
	museum := FindPlace(pointer("Arbetets museum"))
	if last.Destination != "Arbetets museum" || math.Abs(museum.X-last.Target[0]) > 1.3 || math.Abs(museum.Z-last.Target[2]) > 1.3 {
		t.Fatalf("drone %+v", last)
	}
	for _, d := range drones[:8] {
		if strings.TrimSpace(d.Destination) == "" {
			t.Fatalf("%s has no destination", d.ID)
		}
	}
	request.Destination = pointer("Stockholm")
	if err := service.Execute(DispatchCommand{request}); !IsValidation(err) {
		t.Fatalf("Stockholm: %v", err)
	}
}

func TestValidationFailuresLeaveTheProjectionUnchanged(t *testing.T) {
	service := NewDroneService()
	before := service.World.Value()
	zero, badHub := NewDispatchRequest(), NewDispatchRequest()
	zero.Count, badHub.OriginHub = 0, "bad"
	for _, command := range []Command{DispatchCommand{zero}, DispatchCommand{badHub},
		AddZoneCommand{ZoneRequest{[]float64{1.0, 2.0}, 12.0}}, AddZoneCommand{ZoneRequest{[]float64{0.0, 15.0, 0.0}, -1.0}}} {
		if err := service.Execute(command); !IsValidation(err) {
			t.Fatalf("%+v: %v", command, err)
		}
	}
	if service.World.Value() != before {
		t.Fatal("a rejected command published a snapshot")
	}
}

func TestConcurrentCommandsAreSerializedWithoutLostDrones(t *testing.T) {
	service := NewDroneService()
	var wait sync.WaitGroup
	for i := 0; i < 40; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			service.Execute(DispatchCommand{NewDispatchRequest()})
		}()
	}
	wait.Wait()
	ids := map[string]bool{}
	for _, d := range service.World.Value().Drones {
		ids[d.ID] = true
	}
	if len(ids) != 48 || len(service.World.Value().Drones) != 48 {
		t.Fatalf("want 48 distinct drones, got %d of %d", len(ids), len(service.World.Value().Drones))
	}
}

func TestPauseFreezesMovementAndResetClearsTelemetryZonesAndWind(t *testing.T) {
	service := NewDroneService()
	service.Execute(PauseCommand{true})
	before := service.World.Value().Drones
	for i := int64(0); i < 20; i++ {
		service.Step(0.05, i)
	}
	if !reflect.DeepEqual(before, service.World.Value().Drones) {
		t.Fatal("drones moved while paused")
	}
	service.Execute(AddZoneCommand{ZoneRequest{[]float64{0.0, 15.0, 0.0}, 12.0}})
	service.Execute(WindCommand{WindRequest{8.0, 4.0}})
	service.Execute(ResetCommand{})
	world := service.World.Value()
	if len(world.Drones) != 8 || len(world.NoFlyZones) != 0 || world.Paused || !reflect.DeepEqual(world.Wind, []float64{0.0, 0.0}) || service.Telemetry.Value().Deliveries != 0 {
		t.Fatalf("after reset: %+v", world)
	}
}

func TestMissionsDeliverAndARecalledFleetStaysHome(t *testing.T) {
	service := NewDroneService()
	for i := int64(0); i < 600; i++ {
		service.Step(0.05, i)
	}
	if service.Telemetry.Value().Deliveries == 0 {
		t.Fatal("no deliveries after 30 s")
	}
	if service.World.Value().SimulationTime <= 29.9 {
		t.Fatalf("simulation time %v", service.World.Value().SimulationTime)
	}
	service.Execute(RecallCommand{})
	for i := int64(0); i < 1200; i++ {
		service.Step(0.05, i)
	}
	for _, d := range service.World.Value().Drones {
		if d.State != "IDLE" {
			t.Fatalf("%s is %s after the recall", d.ID, d.State)
		}
	}
}

func TestZoneReroutingNeverAllowsADroneInsideExclusionClearance(t *testing.T) {
	service := NewDroneService()
	if err := service.Execute(AddZoneCommand{ZoneRequest{[]float64{0.0, 15.0, 0.0}, 12.0}}); err != nil {
		t.Fatal(err)
	}
	for frame := int64(0); frame < 500; frame++ {
		service.Step(0.05, frame)
		state := service.World.Value()
		for _, d := range state.Drones {
			if p := vecOf(d.Pos); !PathClear(p, p, state.NoFlyZones) {
				t.Fatalf("%s entered a zone", d.ID)
			}
		}
	}
}

func TestParkenAliasesResolveSouthwestAndLjuraSoutheastOfTheCentre(t *testing.T) {
	stadium := FindPlace(pointer("Idrottsparken"))
	if stadium == nil || stadium != FindPlace(pointer(" PARKEN ")) || stadium != FindPlace(pointer("Nya Parken")) || stadium.X >= -30 || stadium.Z <= 20 {
		t.Fatalf("stadium %+v", stadium)
	}
	ljura := FindPlace(pointer("Ljuraparken"))
	near(t, "Ljuraparken x", 35.698, ljura.X, 0.001)
	near(t, "Ljuraparken z", 39.752, ljura.Z, 0.001)
	for _, p := range Places {
		if math.Abs(p.X) > 79 || math.Abs(p.Z) > 79 {
			t.Fatalf("%s is off the map", p.Name)
		}
	}
}

// --- Road routing (RoadRoutingTest.kt) ---

func routingFixture(pairs ...[2]int) *TransportNetwork {
	highest := 0
	for _, p := range pairs {
		highest = max(highest, p[0], p[1])
	}
	var nodes []RoadNode
	for i := 0; i <= highest; i++ {
		nodes = append(nodes, RoadNode{ID: int64(i), X: float64(i), Z: float64(i % 2), Y: .071})
	}
	var edges []RoadEdge
	for _, p := range pairs {
		edges = append(edges, RoadEdge{p[0], p[1], 1.0, .5, .3, true, "Test", 1, true, true})
	}
	return NewTransportNetwork(nodes, edges, nil, nil, nil)
}

func allEdges(n *TransportNetwork) []int {
	edges := make([]int, len(n.Edges))
	for i := range edges {
		edges[i] = i
	}
	return edges
}

func TestThroughRoadEdges(t *testing.T) {
	cases := []struct {
		name       string
		network    *TransportNetwork
		candidates []int
		want       []int
	}{
		{"policy-cut spur is pruned back to its junction but exits remain usable",
			routingFixture([2]int{0, 1}, [2]int{1, 2}, [2]int{2, 0}, [2]int{1, 3}, [2]int{3, 1}, [2]int{3, 4}, [2]int{4, 3}, [2]int{4, 5}, [2]int{5, 4}),
			[]int{0, 1, 2, 3, 4, 5, 6, 8}, []int{0, 1, 2, 4, 6, 8}},
		{"real mapped cul-de-sacs preserve existing traffic and turnaround behavior",
			routingFixture([2]int{0, 1}, [2]int{1, 2}, [2]int{2, 0}, [2]int{1, 3}, [2]int{3, 1}, [2]int{3, 4}, [2]int{4, 3}),
			nil, []int{0, 1, 2, 3, 4, 5, 6}},
		{"parallel reverse ways cannot disguise a dead end",
			routingFixture([2]int{0, 1}, [2]int{1, 2}, [2]int{2, 0}, [2]int{1, 3}, [2]int{3, 1}, [2]int{3, 1}, [2]int{3, 4}),
			[]int{0, 1, 2, 3, 4, 5}, []int{0, 1, 2, 4, 5}},
		{"one-way traps are pruned and legitimate loops and one-way exits survive",
			routingFixture([2]int{0, 1}, [2]int{1, 2}, [2]int{2, 0}, [2]int{1, 3}, [2]int{3, 4}, [2]int{5, 0}),
			nil, []int{0, 1, 2, 5}},
	}
	for _, c := range cases {
		candidates := c.candidates
		if candidates == nil {
			candidates = allEdges(c.network)
		}
		if got := throughRoadEdges(c.network, candidates, nil); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: want %v, got %v", c.name, c.want, got)
		}
	}
}

// noForcedReversal checks that every retained edge has an onward route or ends at a real mapped road end.
func noForcedReversal(t *testing.T, n *TransportNetwork, through *EdgeSet, label string) {
	t.Helper()
	for _, id := range through.List {
		e := &n.Edges[id]
		realEnd, onward := true, false
		for _, next := range n.Outgoing[e.To] {
			realEnd = realEnd && n.Edges[next].To == e.From
			onward = onward || (through.Has(next) && n.Edges[next].To != e.From)
		}
		if !realEnd && !onward {
			t.Fatalf("%s edge %d would force a reversal", label, id)
		}
	}
}

func TestBundledRoutingKeepsConnectionsWithoutForcedReversals(t *testing.T) {
	n := BundledNetwork()
	edge := func(match func(e *RoadEdge) bool) int {
		found := -1
		for i := range n.Edges {
			if match(&n.Edges[i]) {
				if found >= 0 {
					t.Fatal("more than one matching edge")
				}
				found = i
			}
		}
		if found < 0 {
			t.Fatal("no matching edge")
		}
		return found
	}
	car := newEdgeSet(len(n.Edges), throughRoadEdges(n, allEdges(n), nil))
	// The Kungsgatan bridge connects cars through Skvallertorget without a reversal.
	ids := []int64{286409009, 286409016, 120452, 3286754732, 3015112537, 1108233121, 298337070, 12533815721, 11634055968, 12533823572, 11231112376}
	for i := 1; i < len(ids); i++ {
		from, to := ids[i-1], ids[i]
		if !car.Has(edge(func(e *RoadEdge) bool { return n.Nodes[e.From].ID == from && n.Nodes[e.To].ID == to })) {
			t.Fatalf("missing northbound connection %d -> %d", from, to)
		}
	}
	noForcedReversal(t, n, car, "car")
	// Existing underpass routes must still carry traffic.
	for _, way := range []int64{39445117, 89615815} {
		carries := false
		for _, id := range car.List {
			carries = carries || n.Edges[id].Way == way
		}
		if !carries {
			t.Fatalf("underpass way %d carries no traffic", way)
		}
	}
	// Trucks remain excluded from the bridge whose only onward road is closed to them.
	var candidates []int
	for i := range n.Edges {
		if n.Edges[i].Truck {
			candidates = append(candidates, i)
		}
	}
	truck := newEdgeSet(len(n.Edges), throughRoadEdges(n, candidates, nil))
	northbound := edge(func(e *RoadEdge) bool { return e.Way == 26154805 && n.Nodes[e.To].ID == 286409016 })
	if !n.Edges[northbound].Truck || truck.Has(northbound) || len(truck.List) <= 1000 {
		t.Fatalf("truck policy: candidate %v, retained %v, %d edges", n.Edges[northbound].Truck, truck.Has(northbound), len(truck.List))
	}
	noForcedReversal(t, n, truck, "truck")
}

// --- City transport (CityTransportTest.kt, TransitRegressionTest.kt, TransitDestinationTest.kt) ---

func TestRegionalNetworkIncludesAirportsAndExcludesDrottninggatan(t *testing.T) {
	n := BundledNetwork()
	if len(n.Airports) != 2 || n.Airports[0].ID != "ESSP" || n.Airports[1].ID != "F13" || len(n.Nodes) <= 30000 {
		t.Fatalf("airports %v, %d nodes", n.Airports, len(n.Nodes))
	}
	signals := 0
	for i := range n.Nodes {
		if n.Nodes[i].Signal {
			signals++
			if n.Nodes[i].SignalHorizontal == nil {
				t.Fatalf("signal %d has no direction", n.Nodes[i].ID)
			}
		}
	}
	truckRing := false
	for i := range n.Edges {
		e := &n.Edges[i]
		if strings.EqualFold(e.Name, "Drottninggatan") {
			t.Fatal("Drottninggatan is drivable")
		}
		if !(e.Length > 0) || !isFinite(e.Length) || e.From < 0 || e.From >= len(n.Nodes) || e.To < 0 || e.To >= len(n.Nodes) {
			t.Fatalf("broken edge %d", i)
		}
		truckRing = truckRing || (e.Truck && e.Name == "Söderleden")
	}
	if signals <= 50 || !truckRing {
		t.Fatalf("%d signals, trucks on Söderleden %v", signals, truckRing)
	}
	// Underpasses preserve through traffic instead of dead ends.
	for _, way := range []int64{39445117, 89615815} {
		found := false
		for i := range n.Edges {
			if n.Edges[i].Way == way {
				found = true
				if len(n.Outgoing[n.Edges[i].To]) == 0 {
					t.Fatalf("underpass %d ends in a dead end", way)
				}
			}
		}
		if !found {
			t.Fatalf("underpass %d is missing", way)
		}
	}
	routes := map[string][]string{}
	for _, r := range n.TransitRoutes {
		for _, d := range r.Destinations {
			routes[r.ID] = append(routes[r.ID], d.Name)
		}
	}
	if !reflect.DeepEqual(routes["tram-2"], []string{"Kvarnberget", "Fridvalla"}) || !reflect.DeepEqual(routes["tram-3"], []string{"Vidablick", "Klockaretorpet"}) {
		t.Fatalf("tram destinations %v", routes)
	}
}

func TestVehicleCountsRoutesAndTruckRestrictionsSurviveMovement(t *testing.T) {
	n := BundledNetwork()
	traffic := NewBundledRoadTraffic()
	if err := traffic.Configure(TrafficSettings{Compact: 32, Estate: 16, Trucks: 12, TimeScale: 1.0}); err != nil {
		t.Fatal(err)
	}
	initial := traffic.Views()
	truckRoads := map[string]bool{}
	for i := range n.Edges {
		if n.Edges[i].Truck {
			truckRoads[n.Edges[i].Name] = true
		}
	}
	for tick := 0; tick < 1200; tick++ {
		traffic.Tick(.05, float64(tick)*.05)
		for _, v := range traffic.Views() {
			if !isFinite(v.Pos[0]) || !isFinite(v.Pos[1]) || !isFinite(v.Pos[2]) || v.Speed < 0 || v.Speed > 125 || v.Road == "Drottninggatan" {
				t.Fatalf("vehicle %+v", v)
			}
			if v.Kind == "truck" && !truckRoads[v.Road] {
				t.Fatalf("truck on %s", v.Road)
			}
		}
	}
	counts, moved := map[string]int{}, 0
	for i, v := range traffic.Views() {
		counts[v.Kind]++
		if !reflect.DeepEqual(v.Pos, initial[i].Pos) {
			moved++
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"compact": 32, "estate": 16, "truck": 12}) || moved <= 40 {
		t.Fatalf("counts %v, %d moved", counts, moved)
	}
	traffic.Configure(TrafficSettings{TimeScale: 1.0})
	if len(traffic.Views()) != 0 {
		t.Fatal("vehicles remain after configuring none")
	}
}

func node(id int64, x, z float64) RoadNode { return RoadNode{ID: id, X: x, Z: z, Y: .071} }

func oneWay(from, to int, length float64, name string, way int64) RoadEdge {
	return RoadEdge{from, to, length, .5, .3, true, name, way, true, true}
}

func smallTraffic(nodes []RoadNode, edges []RoadEdge) *RoadTraffic {
	return NewRoadTraffic(NewTransportNetwork(nodes, edges, nil, nil, nil), BundledTerrain(), BundledRoadAccess())
}

func TestARedSignalStopsAVehicleUntilItsDirectionGetsGreen(t *testing.T) {
	signal := node(2, 5.0, 0.0)
	signal.Signal = true
	traffic := smallTraffic([]RoadNode{node(1, 0.0, 0.0), signal, node(3, 10.0, 0.0)},
		[]RoadEdge{oneWay(0, 1, 5.0, "Approach", 1), oneWay(1, 2, 5.0, "Exit", 1)})
	traffic.Configure(TrafficSettings{Compact: 1, TimeScale: 1.0})
	for i := 0; i < 500; i++ {
		traffic.Tick(.05, 30.0)
	}
	stopped := traffic.Views()[0]
	if stopped.Road != "Approach" || !stopped.Stopped || stopped.Pos[0] >= 5.0 {
		t.Fatalf("at red: %+v", stopped)
	}
	for i := 0; i < 400; i++ {
		traffic.Tick(.05, 5.0)
	}
	if road := traffic.Views()[0].Road; road != "Exit" {
		t.Fatalf("at green the vehicle is on %s", road)
	}
}

func TestAQueuedApproachRestartsOnGreenIncludingShortIncomingEdges(t *testing.T) {
	signal := node(3, 4.1, 0.0)
	signal.Signal, signal.SignalHorizontal = true, pointer(true)
	traffic := smallTraffic(
		[]RoadNode{node(1, 0.0, 0.0), node(2, 4.0, 0.0), signal, node(4, 10.0, 0.0), node(5, 10.0, 5.0), node(6, 0.0, 5.0)},
		[]RoadEdge{oneWay(0, 1, 4.0, "Queue", 1), oneWay(1, 2, .1, "Short approach", 2), oneWay(2, 3, 5.9, "After signal", 3),
			oneWay(3, 4, 5.0, "Loop", 4), oneWay(4, 5, 10.0, "Loop", 5), oneWay(5, 0, 5.0, "Loop", 6)})
	traffic.Configure(TrafficSettings{Compact: 7, Trucks: 2, TimeScale: 1.0})
	for i := 0; i < 6000; i++ {
		traffic.Tick(.05, 30.0)
	}
	stopped, count := map[int]VehicleView{}, 0
	for _, v := range traffic.Views() {
		stopped[v.ID] = v
		if v.Stopped {
			count++
		}
	}
	if count < 7 {
		t.Fatalf("only %d vehicles queue at red", count)
	}
	for i := 0; i < 2000; i++ {
		traffic.Tick(.05, 5.0)
	}
	moved := 0
	for _, v := range traffic.Views() {
		if math.Hypot(v.Pos[0]-stopped[v.ID].Pos[0], v.Pos[2]-stopped[v.ID].Pos[2]) > 1 {
			moved++
		}
	}
	if moved < 7 {
		t.Fatalf("only %d vehicles restarted on green", moved)
	}
}

func TestRegionalTrafficKeepsFlowingAfterTenMinutes(t *testing.T) {
	traffic := NewBundledRoadTraffic()
	traffic.Configure(TrafficSettings{Compact: 100, Estate: 60, Trucks: 30, TimeScale: 1.0, Buses: 12})
	for i := 0; i < 6000; i++ {
		traffic.Tick(.05, float64(i)*.05)
	}
	before := map[int]VehicleView{}
	for _, v := range traffic.Views() {
		before[v.ID] = v
	}
	for i := 0; i < 6000; i++ {
		traffic.Tick(.05, 300+float64(i)*.05)
	}
	stuck := 0
	for _, v := range traffic.Views() {
		if math.Hypot(v.Pos[0]-before[v.ID].Pos[0], v.Pos[2]-before[v.ID].Pos[2]) < .1 {
			stuck++
		}
	}
	if stuck >= 20 {
		t.Fatalf("%d vehicles are still blocked after five minutes", stuck)
	}
}

func TestBusStopsAgainOnTheNextLap(t *testing.T) {
	stop := node(2, 3.0, 0.0)
	stop.BusStop = pointer("Testhållplats")
	var edges []RoadEdge
	for i := 0; i < 4; i++ {
		edges = append(edges, oneWay(i, (i+1)%4, 3.0, "Bus loop", int64(i)))
	}
	traffic := smallTraffic([]RoadNode{node(1, 0.0, 0.0), stop, node(3, 3.0, 3.0), node(4, 0.0, 3.0)}, edges)
	traffic.Configure(TrafficSettings{TimeScale: 1.0, Buses: 1})
	stops, wasStopped := 0, false
	for i := 0; i < 8000; i++ {
		traffic.Tick(.05, float64(i)*.05)
		stopped := traffic.Views()[0].Stop != nil
		if stopped && !wasStopped {
			stops++
		}
		wasStopped = stopped
	}
	if stops < 3 {
		t.Fatalf("the bus stopped only %d times", stops)
	}
}

func TestFlightTaxisTakesOffAndLandsAtBothAirportsWithoutTeleportation(t *testing.T) {
	flight := NewBundledRegionalFlight()
	phases, arrivals := map[string]bool{}, map[string]bool{}
	previous := flight.View().Pos
	for i := 0; i < 52000; i++ {
		if flight.Tick(.05) {
			arrivals[flight.View().Destination] = true
		}
		v := flight.View()
		phases[v.Phase] = true
		jump := math.Sqrt(math.Pow(v.Pos[0]-previous[0], 2) + math.Pow(v.Pos[1]-previous[1], 2) + math.Pow(v.Pos[2]-previous[2], 2))
		if !isFinite(jump) || jump >= .4 || v.Pos[1] < .099 || v.Speed >= 720 {
			t.Fatalf("jump of %v at height %v and speed %v in %s", jump, v.Pos[1], v.Speed, v.Phase)
		}
		previous = v.Pos
	}
	if len(phases) != 8 || !arrivals["Norrköpings flygplats"] || !arrivals["Bråvalla F13"] || flight.Completed < 2 {
		t.Fatalf("phases %v, arrivals %v, %d completed", phases, arrivals, flight.Completed)
	}
}

func TestPauseFreezesTrafficAndInvalidSettingsDoNotMutateState(t *testing.T) {
	service := NewCityService()
	if err := service.Execute(ConfigureCity{TrafficSettings{Compact: 4, Estate: 3, Trucks: 2, TimeScale: 1.0}}); err != nil {
		t.Fatal(err)
	}
	service.Step(1.0)
	service.Execute(PauseCity{true})
	before := service.World.Value()
	service.Step(1.0)
	after := service.World.Value()
	if !reflect.DeepEqual(before.Vehicles, after.Vehicles) || before.SimulationTime != after.SimulationTime || len(before.Vehicles) != 9 {
		t.Fatalf("paused traffic changed, or %d vehicles", len(before.Vehicles))
	}
	tooMany := DefaultTrafficSettings()
	tooMany.Compact, tooMany.Estate, tooMany.Trucks = 600, 600, 150
	if err := service.Execute(ConfigureCity{tooMany}); !IsValidation(err) {
		t.Fatalf("too many vehicles: %v", err)
	}
	if service.World.Value().Sequence != after.Sequence {
		t.Fatal("a rejected command published a snapshot")
	}
}

func TestArticulatedRailTrafficMovesDwellsAndResumesWithoutJumps(t *testing.T) {
	rail := NewBundledRailTraffic()
	rail.Configure(TrafficSettings{Trams: 4, Trains: 2})
	previous := map[int][]float64{}
	for _, v := range rail.Views() {
		previous[v.ID] = v.Pos
	}
	dwelled, resumed := map[string]bool{}, map[string]bool{}
	for i := 0; i < 18000; i++ {
		rail.Tick(.1)
		for _, v := range rail.Views() {
			if want := RailContract[v.Kind].Sections; len(v.Sections) != want {
				t.Fatalf("%s has %d sections", v.Kind, len(v.Sections))
			}
			for _, s := range v.Sections {
				if !isFinite(s.Pos[0]) || !isFinite(s.Pos[1]) || !isFinite(s.Pos[2]) {
					t.Fatalf("%s section at %v", v.Kind, s.Pos)
				}
			}
			if math.Hypot(v.Pos[0]-previous[v.ID][0], v.Pos[2]-previous[v.ID][2]) >= .16 {
				t.Fatalf("%s jumped", v.Kind)
			}
			if v.Stop != nil {
				dwelled[v.Kind] = true
			}
			if dwelled[v.Kind] && v.Speed > 5 {
				resumed[v.Kind] = true
			}
			previous[v.ID] = v.Pos
		}
	}
	if !resumed["tram"] || !resumed["train"] {
		t.Fatalf("resumed after a stop: %v", resumed)
	}
	rail.Configure(TrafficSettings{})
	if len(rail.Views()) != 0 {
		t.Fatal("rail vehicles remain after configuring none")
	}
}

func TestDestinationChangesAndResetsEachLap(t *testing.T) {
	route := TransitRoute{"tram-2", "tram", "Spårvagn 2", true,
		[][]float64{{0.0, .089, 0.0}, {10.0, .089, 0.0}, {10.0, .089, 10.0}, {0.0, .089, 10.0}, {0.0, .089, 0.0}},
		nil, []TransitStop{{0.0, "Kvarnberget"}, {20.0, "Fridvalla"}}}
	rail := NewRailTraffic([]TransitRoute{route}, BundledTerrain())
	rail.Configure(TrafficSettings{Trams: 1})
	if d := rail.Views()[0].Destination; d == nil || *d != "Kvarnberget" {
		t.Fatalf("first destination %v", d)
	}
	var changes []string
	last := ""
	for i := 0; i < 2500; i++ {
		rail.Tick(.1)
		if d := *rail.Views()[0].Destination; d != last {
			last = d
			changes = append(changes, d)
		}
	}
	if len(changes) < 4 || !reflect.DeepEqual(changes[:4], []string{"Kvarnberget", "Fridvalla", "Kvarnberget", "Fridvalla"}) {
		t.Fatalf("destinations %v", changes)
	}
}

// --- Terrain (TerrainServiceTest.kt, TerrainMeshTest.kt, NetworkProfileTest.kt, ElevationLayersTest.kt) ---

func testTile() TerrainTile {
	return TerrainTile{ID: "test-only", Source: "synthetic fixture", SourceURL: "https://example.invalid", Licence: "test", Acquired: "2026-01-01",
		Kind: "DTM", ResolutionMetres: 1.0, VerticalAccuracyMetres: pointer(.1), VerticalDatum: "RH2000", Processing: "test fixture",
		X: 0.0, Z: 0.0, Step: 1.0, Columns: 2, Rows: 2, Heights: []*float64{pointer(10.0), pointer(20.0), pointer(30.0), pointer(40.0)}}
}

func testTerrain(tiles ...TerrainTile) (*TerrainService, error) {
	reference := GeoReference{"EPSG:3006", 569030.0, 6494989.0, 0.9996584126, -1.0134914515, 25.0, "RH2000", 0.0}
	return NewTerrainService(&TerrainDataset{Version: DatasetVersion, Revision: "test", Reference: reference, Tiles: tiles})
}

func TestTerrainInterpolationNoDataAndPriority(t *testing.T) {
	s, err := testTerrain(testTile())
	if err != nil {
		t.Fatal(err)
	}
	if sample := s.Sample(.5, .5, false); sample.MetresRH2000 != 25.0 || sample.SceneY != 1.0 {
		t.Fatalf("centre %+v", sample)
	}
	if s.Sample(1.0, 1.0, false).MetresRH2000 != 40.0 || s.Sample(-.01, 0.0, false) != nil {
		t.Fatal("corner or outside sample is wrong")
	}
	near(t, "legacy profile", 1.091, s.ProfileHeight([]float64{0.0, .071, 0.0}, []float64{1.0, .071, 1.0}, .5), 1e-12)
	near(t, "absolute profile", 5.0, s.ProfileHeightAbsolute(.5, 100.0, 150.0), 0)

	fallback, holed := testTile(), testTile()
	fallback.ID, fallback.Kind = "fallback", "DSM-fallback"
	fallback.Heights = []*float64{pointer(50.0), pointer(50.0), pointer(50.0), pointer(50.0)}
	holed.Heights = []*float64{pointer(10.0), nil, pointer(30.0), pointer(40.0)}
	s, _ = testTerrain(fallback, holed)
	if s.Sample(0.0, 0.0, false).SourceID != "test-only" || s.Sample(.5, .5, false).SourceID != "fallback" {
		t.Fatal("measured ground should win where it has data, the fallback elsewhere")
	}
	empty, _ := testTerrain()
	if empty.Sample(0.0, 0.0, false) != nil || empty.GroundHeight(0.0, 0.0) != -.02 {
		t.Fatal("no tiles: want no sample and the legacy ground plane")
	}

	otherDatum, broken := testTile(), testTile()
	otherDatum.VerticalDatum = "EGM2008"
	broken.Heights = []*float64{pointer(math.NaN())}
	for _, tile := range []TerrainTile{otherDatum, broken} {
		if _, err := testTerrain(tile); err == nil {
			t.Fatalf("tile %+v should be rejected", tile)
		}
	}

	// Binary grids mark no-data with NaN, and water is a separate layer.
	raw := make([]byte, 16)
	for i, v := range []float32{10, float32(math.NaN()), 30, 40} {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	packed, water := testTile(), testTile()
	packed.Heights, packed.HeightsBase64 = nil, pointer(base64.StdEncoding.EncodeToString(raw))
	water.ID, water.Kind = "water", "water"
	water.Heights = []*float64{pointer(90.0), pointer(90.0), pointer(90.0), pointer(90.0)}
	s, err = testTerrain(packed, water)
	if err != nil {
		t.Fatal(err)
	}
	if s.Sample(0.0, 0.0, false).MetresRH2000 != 10.0 || s.Sample(.5, .5, false) != nil || s.Sample(.5, .5, true).MetresRH2000 != 90.0 {
		t.Fatal("binary no-data or water layer is wrong")
	}
}

// The backend surface is the UI's adaptive terrain mesh (surfaceMesh.ts), so vehicles ride on the drawn
// ground. The fixture holds mesh heights from the reference implementation (UI scripts/geo/rtin.py).
func TestTerrainMeshMatchesTheReferenceHeights(t *testing.T) {
	terrain := BundledTerrain()
	points := fixture(t, "terrain-mesh-control.json")["mesh"].([]any)
	if len(points) < 1000 {
		t.Fatalf("only %d reference points", len(points))
	}
	for i, p := range points {
		v := numbers(p)
		x, z := v[0], v[1]
		near(t, "mesh height", v[2], terrain.SurfaceHeight(x, z)*25, 2e-6)
		near(t, "ground height", terrain.SurfaceHeight(x, z), terrain.GroundHeight(x, z), 1e-12)
		if i < 300 {
			// Tiny steps never jump: the mesh has no cracks (5 m nodes, slopes below 100 %).
			for _, d := range [][2]float64{{1e-7, 0}, {0, 1e-7}, {1e-7, 1e-7}} {
				if math.Abs(terrain.SurfaceHeight(x+d[0], z+d[1])-terrain.SurfaceHeight(x-d[0], z-d[1]))*25 >= 1e-4 {
					t.Fatalf("crack in the mesh at %v, %v", x, z)
				}
			}
		}
	}
}

// Vehicles use the same heights as the drawn network. The fixture holds terrain.ts profileHeight() values
// from the UI for engineered spans over underpasses and culverts, bridge approaches (one profiled end,
// terrain with tapered offsets), decks (both ends profiled, linear), ordinary edges, rail routes over
// spans, and segments absorbed into the road breakline patches.
func TestNetworkProfileReproducesTheUIHeights(t *testing.T) {
	terrain := BundledTerrain()
	control := fixture(t, "network-profile-control.json")
	if control["revision"] != terrain.Data.Revision {
		t.Fatalf("fixture revision %v, terrain %s", control["revision"], terrain.Data.Revision)
	}
	groups := control["groups"].(map[string]any)
	for _, group := range []string{"span", "approach", "deck", "ordinary", "railSpan", "integrated"} {
		rows := groups[group].([]any)
		if len(rows) < 8 {
			t.Fatalf("%s has only %d rows", group, len(rows))
		}
		for _, row := range rows {
			v := numbers(row)
			near(t, group, v[7], terrain.ProfileHeight(v[0:3], v[3:6], v[6]), 2e-7)
		}
	}
	// A span may cover only part of its segment; a deck needs two profiled ends.
	span := numbers(groups["span"].([]any)[0])
	covered := false
	for i := 0; i <= 20; i++ {
		_, ok := terrain.SpanHeight(span[0], span[2], span[3], span[5], float64(i)/20.0)
		covered = covered || ok
	}
	approach, deck := numbers(groups["approach"].([]any)[0]), numbers(groups["deck"].([]any)[0])
	if !covered || terrain.LinearBetween(approach[0:3], approach[3:6]) || !terrain.LinearBetween(deck[0:3], deck[3:6]) {
		t.Fatal("span coverage or deck rule is wrong")
	}
}

// The legacy flat-scene layer lift is gone from network heights (same rule as terrain.ts).
func TestElevationLayers(t *testing.T) {
	terrain, network := BundledTerrain(), BundledNetwork()
	near(t, "street above ground", .097, streetAboveGround, 1e-12)
	checked := 0
	var ordinary *RoadNode
	for i := range network.Nodes {
		n := &network.Nodes[i]
		if _, profiled := terrain.BridgeHeight(n.X, n.Z, n.Y); n.Y != .071 || profiled {
			continue
		}
		if ordinary == nil {
			ordinary = n
		}
		// Reference + drawn road surface (0.006) = rendered DTM surface; cars ride 0.002 (5 cm) above it.
		near(t, "road surface", terrain.GroundHeight(n.X, n.Z), terrain.RoadHeight(n.X, n.Z, n.Y)+.006, 1e-12)
		p := []float64{n.X, n.Y, n.Z}
		near(t, "ride height", .05, (terrain.ProfileHeight(p, p, 0.0)+.008-terrain.GroundHeight(n.X, n.Z))*25, 1e-9)
		if checked++; checked == 500 {
			break
		}
	}
	if checked != 500 {
		t.Fatalf("only %d ordinary nodes", checked)
	}
	// Bridge profiles move with their approach roads.
	keys := make([]string, 0, len(bridgeProfiles().Points))
	for key := range bridgeProfiles().Points {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var x, z, legacy float64
	if err := json.Unmarshal([]byte("["+keys[0]+"]"), &[]*float64{&x, &z, &legacy}); err != nil {
		t.Fatal(err)
	}
	near(t, "bridge profile", bridgeProfiles().Points[keys[0]]-streetAboveGround, terrain.RoadHeight(x, z, legacy), 1e-12)
	// Elevated lifts above the road level are kept; flat-scene dips below it are in the DTM already.
	n := ordinary
	near(t, "elevated lift", .24, terrain.RoadHeight(n.X, n.Z, .311)-terrain.RoadHeight(n.X, n.Z, .071), 1e-12)
	near(t, "dip", terrain.RoadHeight(n.X, n.Z, .071), terrain.RoadHeight(n.X, n.Z, .02), 1e-12)
	// Aircraft: legacy 0.1 on the runway pavement modelled at 0.09.
	first := &network.Nodes[0]
	near(t, "aircraft on the pavement", terrain.GroundHeight(first.X, first.Z)+.01, terrain.PlaceLegacy(first.X, first.Z, .1, .09), 1e-12)
}

// Rail vehicles use the UI's rail track contract (trackProfile.ts). The fixture holds RouteTrack.pose()
// section poses from the UI, including every deck node.
func TestRailTrackReproducesTheUISectionPoses(t *testing.T) {
	terrain := BundledTerrain()
	control := fixture(t, "rail-track-control.json")
	if control["revision"] != terrain.Data.Revision {
		t.Fatalf("fixture revision %v, terrain %s", control["revision"], terrain.Data.Revision)
	}
	head := control["railHeadMetres"].(map[string]any)
	if head["rail"] != RailContract["train"].RailHeadMetres || head["tram"] != RailContract["tram"].RailHeadMetres {
		t.Fatalf("rail head %v", head)
	}
	for kind, v := range control["vehicles"].(map[string]any) {
		want, have := v.(map[string]any), RailContract[kind]
		if want["bogieHalf"] != have.BogieHalfMetres || want["sectionSpacing"] != have.SectionSpacingMetres || want["sections"] != float64(have.Sections) {
			t.Fatalf("%s geometry: fixture %v, contract %+v", kind, want, have)
		}
	}
	routes := map[string]*TransitRoute{}
	for i := range BundledNetwork().TransitRoutes {
		routes[BundledNetwork().TransitRoutes[i].ID] = &BundledNetwork().TransitRoutes[i]
	}
	checked := 0
	for id, rows := range control["routes"].(map[string]any) {
		track := NewRailTrack(routes[id], terrain)
		for _, row := range rows.([]any) {
			v := numbers(row)
			pose := track.Pose(v[0])
			for axis := 0; axis < 3; axis++ {
				near(t, id+" position", v[1+axis], pose.Pos[axis], 1e-7)
			}
			near(t, id+" heading", 0.0, math.Atan2(math.Sin(v[4]-pose.Heading), math.Cos(v[4]-pose.Heading)), 1e-7)
			near(t, id+" pitch", v[5], pose.Pitch, 1e-7)
			checked++
		}
	}
	if checked <= 900 {
		t.Fatalf("only %d poses checked", checked)
	}
}

// --- Projection (Sweref99TmTest.kt) ---

func TestSwerefMatchesLantmaterietReferencePoints(t *testing.T) {
	dms := func(d, m, s float64) float64 { return d + m/60.0 + s/3600.0 }
	references := []struct{ lat, lon, e, n, tolerance float64 }{
		// Lantmäteriet SWEPOS calculation report, point G5TA (values printed to 0.1 mas / 1 mm).
		{dms(60, 39, 59.47023), dms(17, 7, 56.56458), 616524.614, 6727534.361, 0.002},
		// Lantmäteriet STAC tile corners around Norrköping (dtm-cog catalogue footprints).
		{58.54634930783993, 16.030843496253457, 560000.0, 6490000.0, 0.001},
		{58.63464848877832, 16.205695414411846, 570000.0, 6500000.0, 0.001},
		{58.54313334876391, 16.374358339591005, 580000.0, 6490000.0, 0.001},
		{58.72443948868819, 16.20879941017453, 570000.0, 6510000.0, 0.001},
	}
	for _, r := range references {
		e, n := SwerefForward(r.lat, r.lon)
		if math.Hypot(e-r.e, n-r.n) >= r.tolerance {
			t.Fatalf("forward %+v -> %v, %v", r, e, n)
		}
		lat, lon := SwerefInverse(r.e, r.n)
		// 1e-8 degrees is about 1 mm.
		near(t, "latitude", r.lat, lat, 2e-8)
		near(t, "longitude", r.lon, lon, 2e-8)
	}
	terrain, network := BundledTerrain(), BundledNetwork()
	x0, z0 := terrain.FromSweref(569030.0, 6494989.0)
	near(t, "origin x", 0.0, x0, 1e-12)
	near(t, "origin z", 0.0, z0, 1e-12)
	// 1 km of grid easting is 1 km / gridScaleFactor of ground, i.e. 40.0137 scene units.
	x1, _ := terrain.FromSweref(570030.0, 6494989.0)
	near(t, "one kilometre east", 1000.0/25.0/terrain.Data.Reference.GridScaleFactor, x1, 1e-9)
	// Krüger series round trip: a few micrometres (1e-6 units = 25 µm).
	x, z := terrain.Project(terrain.Unproject(12.3, -45.6))
	near(t, "round trip x", 12.3, x, 1e-6)
	near(t, "round trip z", -45.6, z, 1e-6)
	for _, e := range network.Edges[:5000] {
		a, b := network.Nodes[e.From], network.Nodes[e.To]
		near(t, "edge length", math.Hypot(b.X-a.X, b.Z-a.Z), e.Length, 0.0011)
	}
	// Norrköping C platform area in SWEREF 99 TM scene coordinates (geodetic 58.5966 N, 16.1835 E).
	sx, sz := terrain.Project(58.5966, 16.1835)
	found := false
	for i := range network.Nodes {
		found = found || math.Hypot(network.Nodes[i].X-sx, network.Nodes[i].Z-sz) < 4.0
	}
	if !found {
		t.Fatal("no network node near Norrköping C")
	}
}

func TestJavaNumberFormats(t *testing.T) {
	for value, want := range map[float64]string{3: "3.0", 0.25: "0.25", -2.25: "-2.25", 1e-4: "1.0E-4", 1.5e7: "1.5E7", 1234567.5: "1234567.5", 0.001: "0.001"} {
		if got := javaDouble(value); got != want {
			t.Errorf("javaDouble(%v): want %s, got %s", value, want, got)
		}
	}
	for _, c := range []struct {
		value    float64
		decimals int
		want     string
	}{{59.139, 3, "59.139"}, {-0.0004, 3, "-0.000"}, {1.0005, 3, "1.001"}, {0.9995, 3, "1.000"}, {9.9996, 3, "10.000"}, {2.5, 2, "2.50"}, {7, 3, "7.000"}, {-10.467, 3, "-10.467"}, {0.071, 3, "0.071"}} {
		if got := javaFixed(c.value, c.decimals); got != c.want {
			t.Errorf("javaFixed(%v, %d): want %s, got %s", c.value, c.decimals, c.want, got)
		}
	}
}

// BenchmarkCityStepFullLoad measures one 0.1 s service step at the largest allowed load: 1 000 road
// vehicles, 40 trams, 12 trains and the flight at time factor 20 (40 sub-steps), snapshot included.
// The service has 100 ms per step.
func BenchmarkCityStepFullLoad(b *testing.B) {
	city := NewCityService()
	if err := city.Execute(ConfigureCity{TrafficSettings{Compact: 500, Estate: 270, Trucks: 150, Flights: true, TimeScale: 20.0, Buses: 80, Trams: 40, Trains: 12}}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		city.Step(.1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		city.Step(.1)
		city.World.WatchJSON()
	}
}
