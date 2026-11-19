package sim

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
)

// Vec3 is a point or direction in scene units: X east, Y up, Z south.
type Vec3 struct{ X, Y, Z float64 }

func (a Vec3) Add(b Vec3) Vec3    { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3    { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Mul(n float64) Vec3 { return Vec3{a.X * n, a.Y * n, a.Z * n} }
func (a Vec3) Dot(b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Length() float64    { return math.Sqrt(a.Dot(a)) }
func (a Vec3) Array() []float64   { return []float64{a.X, a.Y, a.Z} }
func vecOf(p []float64) Vec3      { return Vec3{p[0], p[1], p[2]} }
func (a Vec3) Normalized() Vec3 {
	if a.Length() < 1e-8 {
		return Vec3{}
	}
	return a.Mul(1 / a.Length())
}

// vecKey compares like a Kotlin data class: by bit pattern, so 0.0 and -0.0 differ.
type vecKey [3]uint64

func (a Vec3) key() vecKey {
	return vecKey{math.Float64bits(a.X), math.Float64bits(a.Y), math.Float64bits(a.Z)}
}

// ValidationError is a rejected command; the API answers 400 with its message.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(message string) error { return &ValidationError{message} }

// IsValidation reports whether err is a rejected command (as opposed to an internal failure).
func IsValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

// Request and response bodies. Field order matches the Kotlin serializers.

type DispatchRequest struct {
	Count       int     `json:"count"`
	OriginHub   string  `json:"originHub"`
	WeightKg    float64 `json:"weightKg"`
	Destination *string `json:"destination"`
}

// NewDispatchRequest returns the request defaults.
func NewDispatchRequest() DispatchRequest {
	return DispatchRequest{Count: 1, OriginHub: "HUB_HAMNEN", WeightKg: 2.0}
}

type ZoneRequest struct {
	Center []float64 `json:"center"`
	Radius float64   `json:"radius"`
}
type PauseRequest struct {
	Paused bool `json:"paused"`
}
type WindRequest struct {
	X float64 `json:"x"`
	Z float64 `json:"z"`
}
type IDRequest struct {
	ID string `json:"id"`
}
type CommandResult struct {
	Message string `json:"message"`
}
type APIError struct {
	Error string `json:"error"`
}

type DroneView struct {
	ID          string      `json:"id"`
	Pos         []float64   `json:"pos"`
	Target      []float64   `json:"target"`
	Battery     float64     `json:"battery"`
	State       string      `json:"state"`
	WeightKg    float64     `json:"weightKg"`
	Route       [][]float64 `json:"route"`
	Destination string      `json:"destination"`
}
type ZoneView struct {
	ID     string    `json:"id"`
	Center []float64 `json:"center"`
	Radius float64   `json:"radius"`
}
type HubView struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Pos   []float64 `json:"pos"`
}
type Activity struct {
	ID      int64   `json:"id"`
	Time    float64 `json:"time"`
	Kind    string  `json:"kind"`
	Message string  `json:"message"`
}
type WorldSnapshot struct {
	Sequence       int64       `json:"sequence"`
	SimulationTime float64     `json:"simulationTime"`
	Paused         bool        `json:"paused"`
	Wind           []float64   `json:"wind"`
	Drones         []DroneView `json:"drones"`
	NoFlyZones     []ZoneView  `json:"noFlyZones"`
	Hubs           []HubView   `json:"hubs"`
}
type TelemetrySnapshot struct {
	Sequence       int64      `json:"sequence"`
	Active         int        `json:"active"`
	Delivering     int        `json:"delivering"`
	Returning      int        `json:"returning"`
	Holding        int        `json:"holding"`
	Deliveries     int        `json:"deliveries"`
	AverageBattery float64    `json:"averageBattery"`
	DistanceMetres float64    `json:"distanceMetres"`
	Events         []Activity `json:"events"`
}

type TrafficSettings struct {
	Compact   int     `json:"compact"`
	Estate    int     `json:"estate"`
	Trucks    int     `json:"trucks"`
	Flights   bool    `json:"flights"`
	TimeScale float64 `json:"timeScale"`
	Buses     int     `json:"buses"`
	Trams     int     `json:"trams"`
	Trains    int     `json:"trains"`
	// Planes is the number of aircraft, 1 to MaxAircraft, when Flights is on. 0 (left out by older
	// clients) keeps the single legacy shuttle NK-01, whose trajectory the Kotlin reference fixes.
	Planes int `json:"planes,omitempty"`
}

// DefaultTrafficSettings are the values a settings request starts from.
func DefaultTrafficSettings() TrafficSettings {
	return TrafficSettings{Compact: 100, Estate: 60, Trucks: 20, Flights: true, TimeScale: 3.0}
}

// VehicleSection is a rail vehicle section: wheel-contact centre on the bogie chord, heading and pitch
// (radians, nose up > 0).
type VehicleSection struct {
	Pos     []float64 `json:"pos"`
	Heading float64   `json:"heading"`
	Pitch   float64   `json:"pitch"`
}
type VehicleView struct {
	ID          int              `json:"id"`
	Kind        string           `json:"kind"`
	Pos         []float64        `json:"pos"`
	Heading     float64          `json:"heading"`
	Speed       float64          `json:"speed"`
	Road        string           `json:"road"`
	Stopped     bool             `json:"stopped"`
	Sections    []VehicleSection `json:"sections"`
	Stop        *string          `json:"stop"`
	Destination *string          `json:"destination"`
	Pitch       float64          `json:"pitch"`
}
type AircraftView struct {
	ID          string    `json:"id"`
	Pos         []float64 `json:"pos"`
	Heading     float64   `json:"heading"`
	Pitch       float64   `json:"pitch"`
	Bank        float64   `json:"bank"`
	Phase       string    `json:"phase"`
	Origin      string    `json:"origin"`
	Destination string    `json:"destination"`
	Speed       float64   `json:"speed"`
	Gear        bool      `json:"gear"`
	// Type is the aircraft type of the fleet (airKinds), Route the name of the route it is cleared for
	// or flying. The single legacy shuttle has neither.
	Type  string `json:"type,omitempty"`
	Route string `json:"route,omitempty"`
}
type CitySnapshot struct {
	Sequence         int64           `json:"sequence"`
	SimulationTime   float64         `json:"simulationTime"`
	Paused           bool            `json:"paused"`
	Settings         TrafficSettings `json:"settings"`
	Vehicles         []VehicleView   `json:"vehicles"`
	Aircraft         []AircraftView  `json:"aircraft"`
	CompletedFlights int             `json:"completedFlights"`
	Events           []Activity      `json:"events"`
	TerrainRevision  string          `json:"terrainRevision"`
}

// State is the read side of a service: the latest immutable snapshot, plus a signal for streams. It
// plays the role of Kotlin's StateFlow. Slow readers skip intermediate snapshots and never block writers.
type State[T any] struct {
	mu      sync.Mutex
	value   *T
	encoded []byte
	changed chan struct{}
}

func newState[T any](initial *T) *State[T] {
	return &State[T]{value: initial, changed: make(chan struct{})}
}

func (s *State[T]) set(v *T) {
	s.mu.Lock()
	s.value, s.encoded = v, nil
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// Value returns the latest snapshot. Snapshots are never modified after publication.
func (s *State[T]) Value() *T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

// Watch returns the latest snapshot and a channel that is closed when a newer one is published.
func (s *State[T]) Watch() (*T, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, s.changed
}

// WatchJSON is Watch with the snapshot as JSON. It is encoded once per snapshot, however many readers.
func (s *State[T]) WatchJSON() (*T, []byte, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.encoded == nil {
		encoded, err := json.Marshal(s.value)
		if err != nil {
			encoded = []byte(`{"error":"snapshot could not be encoded"}`)
		}
		s.encoded = encoded
	}
	return s.value, s.encoded, s.changed
}
