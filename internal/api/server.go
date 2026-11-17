// Package api is the HTTP surface of the simulator: JSON queries, JSON commands and server-sent events.
// Routes, status codes and bodies match the earlier Kotlin/Ktor server, so the UI needs no changes.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"

	"norrkoping-simulator/internal/sim"
)

// Server routes requests to the city simulation and the legacy drone simulation.
type Server struct {
	City   *sim.CityService
	Drones *sim.DroneService
	// AllowedOrigin is the browser origin that may send commands, besides http://127.0.0.1:5173.
	AllowedOrigin string
}

// NewServer reads ALLOWED_ORIGIN like the Kotlin server did.
func NewServer(city *sim.CityService, drones *sim.DroneService) *Server {
	origin := os.Getenv("ALLOWED_ORIGIN")
	if origin == "" {
		origin = "http://localhost:5173"
	}
	return &Server{City: city, Drones: drones, AllowedOrigin: origin}
}

const maxBody = 1 << 20

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "encoding failed", http.StatusInternalServerError)
		return
	}
	writeRaw(w, status, raw)
}

func writeRaw(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	w.WriteHeader(status)
	w.Write(raw)
}

// guard admits same-origin browser commands; CLI clients without Origin are supported on the loopback
// server. Commands must be JSON, which keeps HTML forms out.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if origin := r.Header.Get("Origin"); origin != "" && origin != s.AllowedOrigin && origin != "http://127.0.0.1:5173" {
				writeJSON(w, http.StatusForbidden, sim.APIError{Error: "Origin is not allowed."})
				return
			}
			if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
				writeJSON(w, http.StatusUnsupportedMediaType, sim.APIError{Error: "Use application/json."})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

var errMalformed = errors.New("malformed command body")

// field describes one property of a command body.
type field struct {
	target   any
	required bool
	nullable bool
}

// decode reads a JSON object strictly, as the Kotlin serializers did: unknown properties, missing
// required properties, null for a non-nullable property and wrong types are all malformed.
func decode(r *http.Request, fields map[string]field) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		return errMalformed
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return errMalformed
	}
	for name, value := range object {
		f, known := fields[name]
		if !known {
			return errMalformed
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			if !f.nullable {
				return errMalformed
			}
			continue
		}
		if err := json.Unmarshal(value, f.target); err != nil {
			return errMalformed
		}
	}
	for name, f := range fields {
		if _, present := object[name]; f.required && !present {
			return errMalformed
		}
	}
	return nil
}

// command runs a command handler and answers with its message, or with the reason it was rejected.
func command(message string, run func(r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch err := run(r); {
		case err == nil:
			writeJSON(w, http.StatusOK, sim.CommandResult{Message: message})
		case errors.Is(err, errMalformed):
			writeJSON(w, http.StatusBadRequest, sim.APIError{Error: "Malformed command body."})
		case sim.IsValidation(err):
			writeJSON(w, http.StatusBadRequest, sim.APIError{Error: err.Error()})
		default:
			log.Printf("%s %s failed: %v", r.Method, r.URL.Path, err)
			writeJSON(w, http.StatusInternalServerError, sim.APIError{Error: "Internal error."})
		}
	}
}

// stream sends every published snapshot as a server-sent event. A slow client skips to the latest
// snapshot; it never holds the simulation back.
func stream[T any](state *sim.State[T], event string, sequence func(*T) int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming is not supported", http.StatusInternalServerError)
			return
		}
		header := w.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-store")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for {
			value, encoded, changed := state.WatchJSON()
			if _, err := fmt.Fprintf(w, "event: %s\r\ndata: %s\r\nid: %d\r\nretry: 1500\r\n\r\n", event, encoded, sequence(value)); err != nil {
				return
			}
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-changed:
			}
		}
	}
}

func query[T any](state *sim.State[T]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, encoded, _ := state.WatchJSON()
		writeRaw(w, http.StatusOK, encoded)
	}
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	city, drones := s.City, s.Drones
	api := http.NewServeMux()

	api.Handle("GET /api/simulator/world", query(city.World))
	api.Handle("GET /api/simulator/stream", stream(city.World, "city", func(v *sim.CitySnapshot) int64 { return v.Sequence }))
	api.Handle("POST /api/simulator/settings", command("Trafiken uppdaterad.", func(r *http.Request) error {
		v := sim.DefaultTrafficSettings()
		var compact, estate, trucks, buses, trams, trains int32 = int32(v.Compact), int32(v.Estate), int32(v.Trucks), 0, 0, 0
		if err := decode(r, map[string]field{
			"compact": {target: &compact}, "estate": {target: &estate}, "trucks": {target: &trucks},
			"flights": {target: &v.Flights}, "timeScale": {target: &v.TimeScale},
			"buses": {target: &buses}, "trams": {target: &trams}, "trains": {target: &trains},
		}); err != nil {
			return err
		}
		v.Compact, v.Estate, v.Trucks, v.Buses, v.Trams, v.Trains = int(compact), int(estate), int(trucks), int(buses), int(trams), int(trains)
		return city.Execute(sim.ConfigureCity{Settings: v})
	}))
	api.Handle("POST /api/simulator/pause", command("Paus uppdaterad.", func(r *http.Request) error {
		var v sim.PauseRequest
		if err := decode(r, map[string]field{"paused": {target: &v.Paused, required: true}}); err != nil {
			return err
		}
		return city.Execute(sim.PauseCity{Paused: v.Paused})
	}))
	api.Handle("POST /api/simulator/reset", command("Simuleringen återställd.", func(*http.Request) error {
		return city.Execute(sim.ResetCity{})
	}))

	// The legacy drone endpoints remain for compatibility; the current UI does not use them.
	api.Handle("GET /api/world", query(drones.World))
	api.Handle("GET /api/telemetry", query(drones.Telemetry))
	api.Handle("GET /api/sse/world", stream(drones.World, "world", func(v *sim.WorldSnapshot) int64 { return v.Sequence }))
	api.Handle("GET /api/sse/telemetry", stream(drones.Telemetry, "telemetry", func(v *sim.TelemetrySnapshot) int64 { return v.Sequence }))
	api.Handle("POST /api/commands/dispatch-drone", command("Drones dispatched.", func(r *http.Request) error {
		v := sim.NewDispatchRequest()
		count := int32(v.Count)
		if err := decode(r, map[string]field{
			"count": {target: &count}, "originHub": {target: &v.OriginHub}, "weightKg": {target: &v.WeightKg},
			"destination": {target: &v.Destination, nullable: true},
		}); err != nil {
			return err
		}
		v.Count = int(count)
		return drones.Execute(sim.DispatchCommand{Request: v})
	}))
	api.Handle("POST /api/commands/spawn-no-fly-zone", command("Zone activated.", func(r *http.Request) error {
		v := sim.ZoneRequest{Radius: 12.0}
		if err := decode(r, map[string]field{"center": {target: &v.Center, required: true}, "radius": {target: &v.Radius}}); err != nil {
			return err
		}
		return drones.Execute(sim.AddZoneCommand{Request: v})
	}))
	api.Handle("POST /api/commands/remove-no-fly-zone", command("Zone removed.", func(r *http.Request) error {
		var v sim.IDRequest
		if err := decode(r, map[string]field{"id": {target: &v.ID, required: true}}); err != nil {
			return err
		}
		return drones.Execute(sim.RemoveZoneCommand{ID: v.ID})
	}))
	api.Handle("POST /api/commands/pause", command("Pause updated.", func(r *http.Request) error {
		var v sim.PauseRequest
		if err := decode(r, map[string]field{"paused": {target: &v.Paused, required: true}}); err != nil {
			return err
		}
		return drones.Execute(sim.PauseCommand{Paused: v.Paused})
	}))
	api.Handle("POST /api/commands/wind", command("Wind updated.", func(r *http.Request) error {
		var v sim.WindRequest
		if err := decode(r, map[string]field{"x": {target: &v.X}, "z": {target: &v.Z}}); err != nil {
			return err
		}
		return drones.Execute(sim.WindCommand{Request: v})
	}))
	api.Handle("POST /api/commands/recall", command("Fleet recalled.", func(*http.Request) error {
		return drones.Execute(sim.RecallCommand{})
	}))
	api.Handle("POST /api/commands/reset", command("Simulation reset.", func(*http.Request) error {
		return drones.Execute(sim.ResetCommand{})
	}))

	root := http.NewServeMux()
	root.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	root.Handle("/api/", s.guard(api))
	return root
}
