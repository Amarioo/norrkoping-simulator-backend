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
	"strings"

	"norrkoping-simulator/internal/sim"
)

// Server routes requests to the city simulation and the legacy drone simulation.
type Server struct {
	City   *sim.CityService
	Drones *sim.DroneService
	// Sessions holds the visitors' own cities (?session=<id> on /api/simulator/*). Without it, and for
	// requests without the parameter, everybody shares City.
	Sessions *sim.CitySessions
	// AllowedOrigin is the browser origin that may send commands and, from another domain, read the
	// answers and streams (CORS), besides http://127.0.0.1:5173. Several origins are separated by commas.
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

// allowed reports whether a browser page from origin may use the API.
func (s *Server) allowed(origin string) bool {
	if origin == "http://127.0.0.1:5173" {
		return true
	}
	for candidate := range strings.SplitSeq(s.AllowedOrigin, ",") {
		if strings.TrimSuffix(strings.TrimSpace(candidate), "/") == origin {
			return true
		}
	}
	return false
}

// guard admits browser commands from the UI's origins; clients without Origin (command line, the UI's
// own proxy) are supported. Commands must be JSON, which keeps HTML forms out. A UI served from another
// domain than the API (ALLOWED_ORIGIN) is told that it may read the answers, and its preflight
// requests are answered here.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := origin != "" && s.allowed(origin)
		w.Header().Add("Vary", "Origin")
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		if r.Method == http.MethodOptions {
			if !allowed {
				writeJSON(w, http.StatusForbidden, sim.APIError{Error: "Origin is not allowed."})
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodPost {
			if origin != "" && !allowed {
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

const citiesFull = "Simulatorn har fullt med besökare just nu. Försök igen om en stund."

// cityFor is the visitor's own city when the request names one with ?session=<id>, else the shared city.
func (s *Server) cityFor(r *http.Request) (*sim.CityService, error) {
	id := r.URL.Query().Get("session")
	if id == "" || s.Sessions == nil {
		return s.City, nil
	}
	return s.Sessions.City(id)
}

// refuse answers a query or stream whose city could not be had.
func refuse(w http.ResponseWriter, err error) {
	if errors.Is(err, sim.ErrCitiesFull) {
		writeJSON(w, http.StatusServiceUnavailable, sim.APIError{Error: citiesFull})
		return
	}
	writeJSON(w, http.StatusBadRequest, sim.APIError{Error: err.Error()})
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
		case errors.Is(err, sim.ErrCitiesFull):
			writeJSON(w, http.StatusServiceUnavailable, sim.APIError{Error: citiesFull})
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
	drones := s.Drones
	api := http.NewServeMux()

	api.HandleFunc("GET /api/simulator/world", func(w http.ResponseWriter, r *http.Request) {
		city, err := s.cityFor(r)
		if err != nil {
			refuse(w, err)
			return
		}
		query(city.World)(w, r)
	})
	api.HandleFunc("GET /api/simulator/stream", func(w http.ResponseWriter, r *http.Request) {
		city := s.City
		if id := r.URL.Query().Get("session"); id != "" && s.Sessions != nil {
			own, release, err := s.Sessions.Listen(id)
			if err != nil {
				refuse(w, err)
				return
			}
			defer release()
			city = own
		}
		stream(city.World, "city", func(v *sim.CitySnapshot) int64 { return v.Sequence })(w, r)
	})
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
		city, err := s.cityFor(r)
		if err != nil {
			return err
		}
		return city.Execute(sim.ConfigureCity{Settings: v})
	}))
	api.Handle("POST /api/simulator/pause", command("Paus uppdaterad.", func(r *http.Request) error {
		var v sim.PauseRequest
		if err := decode(r, map[string]field{"paused": {target: &v.Paused, required: true}}); err != nil {
			return err
		}
		city, err := s.cityFor(r)
		if err != nil {
			return err
		}
		return city.Execute(sim.PauseCity{Paused: v.Paused})
	}))
	api.Handle("POST /api/simulator/reset", command("Simuleringen återställd.", func(r *http.Request) error {
		city, err := s.cityFor(r)
		if err != nil {
			return err
		}
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
