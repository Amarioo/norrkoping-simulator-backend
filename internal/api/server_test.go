package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"norrkoping-simulator/internal/sim"
)

// The services are built once: loading the bundled map takes most of a second.
var services = sync.OnceValues(func() (*sim.CityService, *sim.DroneService) {
	return sim.NewCityService(), sim.NewDroneService()
})

func start(t *testing.T) *httptest.Server {
	t.Helper()
	city, drones := services()
	server := httptest.NewServer((&Server{City: city, Drones: drones, AllowedOrigin: "http://localhost:5173"}).Handler())
	t.Cleanup(server.Close)
	return server
}

type request struct {
	body        string
	contentType string
	origin      string
}

func jsonBody(body string) request { return request{body: body, contentType: "application/json"} }

func post(t *testing.T, server *httptest.Server, path string, r request) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(r.body))
	if err != nil {
		t.Fatal(err)
	}
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	if r.origin != "" {
		req.Header.Set("Origin", r.origin)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	json.NewDecoder(response.Body).Decode(&body)
	return response.StatusCode, body
}

func get(t *testing.T, server *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	response, err := http.Get(server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	json.NewDecoder(response.Body).Decode(&body)
	return response.StatusCode, body
}

func expectStatus(t *testing.T, label string, want, got int) {
	t.Helper()
	if want != got {
		t.Fatalf("%s: want status %d, got %d", label, want, got)
	}
}

func TestDroneCommandsUpdateTheReadModelAndRejectMalformedInputs(t *testing.T) {
	server := start(t)
	status, health := get(t, server, "/health")
	expectStatus(t, "health", 200, status)
	if health["status"] != "ok" {
		t.Fatalf("health: %v", health)
	}
	post(t, server, "/api/commands/reset", jsonBody("{}"))
	status, result := post(t, server, "/api/commands/dispatch-drone", jsonBody(`{"count":3,"originHub":"HUB_VRINNEVI","weightKg":1.5}`))
	expectStatus(t, "dispatch", 200, status)
	if result["message"] != "Drones dispatched." {
		t.Fatalf("dispatch: %v", result)
	}
	_, world := get(t, server, "/api/world")
	if n := len(world["drones"].([]any)); n != 11 {
		t.Fatalf("want 11 drones, got %d", n)
	}
	if id := world["hubs"].([]any)[0].(map[string]any)["id"]; id != "HUB_HAMNEN" {
		t.Fatalf("first hub: %v", id)
	}
	status, _ = post(t, server, "/api/commands/dispatch-drone", jsonBody(`{"count":1,"destination":"Arbetets museum"}`))
	expectStatus(t, "addressed dispatch", 200, status)
	_, world = get(t, server, "/api/world")
	if destination := world["drones"].([]any)[11].(map[string]any)["destination"]; destination != "Arbetets museum" {
		t.Fatalf("destination: %v", destination)
	}
	status, _ = post(t, server, "/api/commands/dispatch-drone", jsonBody(`{"count":1,"destination":"Stockholm"}`))
	expectStatus(t, "unknown destination", 400, status)
	status, invalid := post(t, server, "/api/commands/spawn-no-fly-zone", jsonBody(`{"center":[0,15],"radius":12}`))
	expectStatus(t, "two coordinates", 400, status)
	if invalid["error"] != "Center must contain exactly three finite coordinates." {
		t.Fatalf("zone error: %v", invalid)
	}
	for label, body := range map[string]string{
		"truncated": "{", "unknown property": `{"count":1,"colour":"red"}`, "wrong type": `{"count":"three"}`,
		"fraction for an integer": `{"count":1.5}`, "null for a number": `{"count":null}`, "array": `[1]`, "empty": "",
	} {
		status, malformed := post(t, server, "/api/commands/dispatch-drone", jsonBody(body))
		expectStatus(t, label, 400, status)
		if malformed["error"] != "Malformed command body." {
			t.Fatalf("%s: %v", label, malformed)
		}
	}
	status, _ = post(t, server, "/api/commands/dispatch-drone", jsonBody(`{"count":1,"destination":null}`))
	expectStatus(t, "null destination is allowed", 200, status)
	status, _ = post(t, server, "/api/commands/pause", jsonBody(`{}`))
	expectStatus(t, "pause without its required property", 400, status)
	_, telemetry := get(t, server, "/api/telemetry")
	if _, ok := telemetry["averageBattery"]; !ok {
		t.Fatalf("telemetry: %v", telemetry)
	}
}

func TestCrossOriginWritesAndFormPostsAreRejected(t *testing.T) {
	server := start(t)
	for _, path := range []string{"/api/commands/reset", "/api/simulator/reset"} {
		status, body := post(t, server, path, request{body: "{}", contentType: "application/json", origin: "https://unrelated.example"})
		expectStatus(t, "cross origin "+path, 403, status)
		if body["error"] != "Origin is not allowed." {
			t.Fatalf("cross origin: %v", body)
		}
		status, _ = post(t, server, path, request{body: "reset=true", contentType: "application/x-www-form-urlencoded"})
		expectStatus(t, "form post "+path, 415, status)
		status, _ = post(t, server, path, request{body: "{}"})
		expectStatus(t, "no content type "+path, 415, status)
		for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:5173"} {
			status, _ = post(t, server, path, request{body: "{}", contentType: "application/json; charset=utf-8", origin: origin})
			expectStatus(t, "UI origin "+origin+" "+path, 200, status)
		}
	}
}

func TestSimulatorSettingsUpdateTheReadModelAndRejectInvalidCommands(t *testing.T) {
	server := start(t)
	status, result := post(t, server, "/api/simulator/settings", jsonBody(`{"compact":7,"estate":3,"trucks":2,"flights":false,"timeScale":1}`))
	expectStatus(t, "settings", 200, status)
	if result["message"] != "Trafiken uppdaterad." {
		t.Fatalf("settings: %v", result)
	}
	_, world := get(t, server, "/api/simulator/world")
	if n := len(world["vehicles"].([]any)); n != 12 {
		t.Fatalf("want 12 vehicles, got %d", n)
	}
	if n := len(world["aircraft"].([]any)); n != 0 {
		t.Fatalf("want no aircraft, got %d", n)
	}
	if world["terrainRevision"] == "" || world["events"] == nil {
		t.Fatalf("snapshot lacks fields: %v", world)
	}
	vehicle := world["vehicles"].([]any)[0].(map[string]any)
	for _, name := range []string{"id", "kind", "pos", "heading", "speed", "road", "stopped", "sections", "stop", "destination", "pitch"} {
		if _, present := vehicle[name]; !present {
			t.Fatalf("vehicle lacks %q: %v", name, vehicle)
		}
	}
	status, tooMany := post(t, server, "/api/simulator/settings", jsonBody(`{"compact":600,"estate":600,"trucks":150}`))
	expectStatus(t, "too many vehicles", 400, status)
	if !strings.HasPrefix(tooMany["error"].(string), "Välj högst 1 000 vägfordon") {
		t.Fatalf("too many vehicles: %v", tooMany)
	}
	status, _ = post(t, server, "/api/simulator/settings", jsonBody(`{"timeScale":50}`))
	expectStatus(t, "time scale", 400, status)
	status, _ = post(t, server, "/api/simulator/settings", jsonBody("{"))
	expectStatus(t, "truncated", 400, status)
	status, _ = post(t, server, "/api/simulator/pause", jsonBody(`{"paused":true}`))
	expectStatus(t, "pause", 200, status)
	_, paused := get(t, server, "/api/simulator/world")
	if paused["paused"] != true || len(paused["vehicles"].([]any)) != 12 {
		t.Fatalf("paused world: paused %v, %d vehicles", paused["paused"], len(paused["vehicles"].([]any)))
	}
	status, _ = post(t, server, "/api/simulator/reset", jsonBody("{}"))
	expectStatus(t, "reset", 200, status)
	status, _ = get(t, server, "/api/simulator/nothing")
	expectStatus(t, "unknown route", 404, status)
}

func TestStreamSendsTheLatestSnapshotAndThenEveryChange(t *testing.T) {
	server := start(t)
	city, _ := services()
	response, err := http.Get(server.URL + "/api/simulator/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if media := response.Header.Get("Content-Type"); media != "text/event-stream" {
		t.Fatalf("content type %q", media)
	}
	reader := bufio.NewReader(response.Body)
	event := func() (name string, id string, data map[string]any) {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("stream ended: %v", err)
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				return
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(line[6:]), &data); err != nil {
					t.Fatalf("event data is not JSON: %v", err)
				}
			case strings.HasPrefix(line, "event: "):
				name = line[7:]
			case strings.HasPrefix(line, "id: "):
				id = line[4:]
			}
		}
	}
	name, id, first := event()
	if name != "city" || id == "" || first["sequence"] == nil {
		t.Fatalf("first event: %s %s %v", name, id, first)
	}
	city.Step(.1)
	_, _, second := event()
	if second["sequence"].(float64) <= first["sequence"].(float64) {
		t.Fatalf("sequence did not advance: %v then %v", first["sequence"], second["sequence"])
	}
	response.Body.Close()
	io.Copy(io.Discard, response.Body)
}
