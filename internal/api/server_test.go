package api

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

func startWithSessions(t *testing.T, max int, idle time.Duration) (*httptest.Server, *sim.CitySessions) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	city, drones := services()
	sessions := sim.NewCitySessions(ctx, max, idle)
	server := httptest.NewServer((&Server{City: city, Drones: drones, Sessions: sessions, AllowedOrigin: "http://localhost:5173"}).Handler())
	t.Cleanup(server.Close)
	return server, sessions
}

func trucks(t *testing.T, server *httptest.Server, path string) (float64, bool) {
	t.Helper()
	status, world := get(t, server, path)
	expectStatus(t, path, 200, status)
	return world["settings"].(map[string]any)["trucks"].(float64), world["paused"].(bool)
}

func TestEachVisitorHasACityOfTheirOwn(t *testing.T) {
	server, sessions := startWithSessions(t, 2, time.Hour)
	sharedTrucks, sharedPaused := trucks(t, server, "/api/simulator/world")

	status, _ := post(t, server, "/api/simulator/settings?session=visitor-a", jsonBody(`{"compact":5,"estate":4,"trucks":3,"flights":false,"timeScale":2}`))
	expectStatus(t, "settings for a", 200, status)
	status, _ = post(t, server, "/api/simulator/pause?session=visitor-a", jsonBody(`{"paused":true}`))
	expectStatus(t, "pause for a", 200, status)

	if n, paused := trucks(t, server, "/api/simulator/world?session=visitor-a"); n != 3 || !paused {
		t.Fatalf("a: want 3 trucks and paused, got %v, %v", n, paused)
	}
	if n, paused := trucks(t, server, "/api/simulator/world?session=visitor-b"); n != 20 || paused {
		t.Fatalf("b: want the start values (20 trucks, running), got %v, %v", n, paused)
	}
	if n, paused := trucks(t, server, "/api/simulator/world"); n != sharedTrucks || paused != sharedPaused {
		t.Fatalf("shared city changed: %v, %v", n, paused)
	}
	if sessions.Count() != 2 {
		t.Fatalf("want 2 visitor cities, got %d", sessions.Count())
	}

	status, full := get(t, server, "/api/simulator/world?session=visitor-c")
	expectStatus(t, "third visitor of two", 503, status)
	if full["error"] != citiesFull {
		t.Fatalf("full: %v", full)
	}
	status, _ = post(t, server, "/api/simulator/reset?session=visitor-c", jsonBody("{}"))
	expectStatus(t, "command from the third visitor", 503, status)
	response, err := http.Get(server.URL + "/api/simulator/stream?session=visitor-c")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	expectStatus(t, "stream for the third visitor", 503, response.StatusCode)

	status, _ = get(t, server, "/api/simulator/world?session=a/b")
	expectStatus(t, "malformed id", 400, status)
	status, _ = get(t, server, "/api/simulator/world?session=short")
	expectStatus(t, "short id", 400, status)
}

func TestAVisitorCityLivesWhileItIsListenedToAndIsThenRemoved(t *testing.T) {
	server, sessions := startWithSessions(t, 1, 50*time.Millisecond)
	response, err := http.Get(server.URL + "/api/simulator/stream?session=visitor-a")
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, "stream", 200, response.StatusCode)
	reader := bufio.NewReader(response.Body)
	sequences := []float64{}
	for len(sequences) < 3 { // the city runs: the sequence grows
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var snapshot map[string]any
			if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
				t.Fatal(err)
			}
			sequences = append(sequences, snapshot["sequence"].(float64))
		}
	}
	if !(sequences[0] < sequences[1] && sequences[1] < sequences[2]) {
		t.Fatalf("sequences: %v", sequences)
	}
	time.Sleep(300 * time.Millisecond) // far past the idle time, but listened to
	if sessions.Count() != 1 {
		t.Fatalf("a listened city was removed")
	}
	response.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for sessions.Count() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the city was not removed after its listener left")
		}
		time.Sleep(20 * time.Millisecond)
	}
	status, _ := get(t, server, "/api/simulator/world?session=visitor-b")
	expectStatus(t, "the place is free again", 200, status)
}

func TestAUIOnAnotherDomainMayReadAndCommand(t *testing.T) {
	city, drones := services()
	server := httptest.NewServer((&Server{City: city, Drones: drones, AllowedOrigin: "https://sim.example, https://other.example/"}).Handler())
	t.Cleanup(server.Close)
	do := func(method, path, origin string, headers map[string]string, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response
	}
	const allow = "Access-Control-Allow-Origin"
	for _, origin := range []string{"https://sim.example", "https://other.example", "http://127.0.0.1:5173"} {
		read := do("GET", "/api/simulator/world", origin, nil, "")
		expectStatus(t, "read from "+origin, 200, read.StatusCode)
		if got := read.Header.Get(allow); got != origin {
			t.Fatalf("read from %s: %s = %q", origin, allow, got)
		}
		preflight := do("OPTIONS", "/api/simulator/pause", origin, map[string]string{"Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "content-type"}, "")
		expectStatus(t, "preflight from "+origin, 204, preflight.StatusCode)
		if preflight.Header.Get(allow) != origin || preflight.Header.Get("Access-Control-Allow-Methods") != "GET, POST" ||
			preflight.Header.Get("Access-Control-Allow-Headers") != "Content-Type" {
			t.Fatalf("preflight from %s: %v", origin, preflight.Header)
		}
		command := do("POST", "/api/simulator/pause", origin, map[string]string{"Content-Type": "application/json"}, `{"paused":false}`)
		expectStatus(t, "command from "+origin, 200, command.StatusCode)
		if command.Header.Get(allow) != origin {
			t.Fatalf("command from %s: no %s", origin, allow)
		}
	}
	stream, err := http.NewRequest("GET", server.URL+"/api/simulator/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream.Header.Set("Origin", "https://sim.example")
	response, err := http.DefaultClient.Do(stream)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.Header.Get(allow) != "https://sim.example" {
		t.Fatalf("stream: no %s", allow)
	}

	stranger := do("GET", "/api/simulator/world", "https://unrelated.example", nil, "")
	if got := stranger.Header.Get(allow); got != "" {
		t.Fatalf("a stranger may read: %q", got)
	}
	expectStatus(t, "preflight from a stranger", 403, do("OPTIONS", "/api/simulator/pause", "https://unrelated.example", nil, "").StatusCode)
	expectStatus(t, "command from a stranger", 403, do("POST", "/api/simulator/pause", "https://unrelated.example", map[string]string{"Content-Type": "application/json"}, `{"paused":false}`).StatusCode)
	if got := do("GET", "/api/simulator/world", "", nil, "").Header.Get(allow); got != "" {
		t.Fatalf("no origin: %q", got)
	}
}

func TestTheStreamIsCompressedForClientsThatAcceptIt(t *testing.T) {
	server := start(t)
	open := func(encoding string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest("GET", server.URL+"/api/simulator/stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		if encoding != "" {
			req.Header.Set("Accept-Encoding", encoding) // set by hand: the client does not decompress
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { response.Body.Close() })
		var body io.Reader = response.Body
		if response.Header.Get("Content-Encoding") == "gzip" {
			if body, err = gzip.NewReader(response.Body); err != nil {
				t.Fatal(err)
			}
		}
		line, err := bufio.NewReader(body).ReadString('\n') // the first event arrives without waiting for more
		if err != nil {
			t.Fatal(err)
		}
		return response, strings.TrimRight(line, "\r\n")
	}
	if response, line := open("gzip, deflate, br"); response.Header.Get("Content-Encoding") != "gzip" || line != "event: city" {
		t.Fatalf("browser: encoding %q, first line %q", response.Header.Get("Content-Encoding"), line)
	}
	if response, line := open("identity"); response.Header.Get("Content-Encoding") != "" || line != "event: city" {
		t.Fatalf("plain: encoding %q, first line %q", response.Header.Get("Content-Encoding"), line)
	}
	if response, line := open(""); !response.Uncompressed || line != "event: city" {
		t.Fatalf("Go client: uncompressed %v, first line %q", response.Uncompressed, line)
	}
}
