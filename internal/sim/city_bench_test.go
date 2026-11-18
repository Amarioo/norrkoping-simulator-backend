package sim

import (
	"runtime"
	"testing"
)

// The cost of one city: what a visitor's own simulation adds to the server. The real loop calls
// Step(.1) ten times a second, so ns/op × 10 is the processor time per second of wall time.

func benchmarkCityStep(b *testing.B, settings TrafficSettings) {
	city := NewCityService()
	if err := city.Execute(ConfigureCity{Settings: settings}); err != nil {
		b.Fatal(err)
	}
	for range 600 { // a minute of wall time, so the traffic has spread out
		city.Step(.1)
	}
	b.ResetTimer()
	for b.Loop() {
		city.Step(.1)
		city.World.WatchJSON() // one listener: the snapshot is encoded once per step
	}
}

func startSettings() TrafficSettings {
	v := DefaultTrafficSettings()
	v.Buses, v.Trams, v.Trains = 12, 8, 2
	return v
}

func BenchmarkCityStepDefault(b *testing.B) { benchmarkCityStep(b, startSettings()) }

func BenchmarkCityStepHeaviest(b *testing.B) {
	benchmarkCityStep(b, TrafficSettings{Compact: 425, Estate: 425, Trucks: 150, Buses: 0, Trams: 40, Trains: 12, Flights: true, TimeScale: 20})
}

func BenchmarkNewCity(b *testing.B) {
	NewCityService() // the bundled map is loaded once per process
	b.ResetTimer()
	for b.Loop() {
		NewCityService()
	}
}

// TestCityMemory reports the live heap one more city holds (go test -run CityMemory -v).
func TestCityMemory(t *testing.T) {
	NewCityService()
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	before := heap()
	cities := make([]*CityService, 10)
	for i := range cities {
		cities[i] = NewCityService()
		for range 100 {
			cities[i].Step(.1)
		}
	}
	after := heap()
	t.Logf("live heap per city: %.2f MB", float64(after-before)/float64(len(cities))/1e6)
	runtime.KeepAlive(cities)
}
