# Norrköping Simulator — Go

A local city simulation for the Norrköping Simulator UI in `../norrkoping-simulator-ui`. Cars use a directed OpenStreetMap road graph. Up to six fictional regional aircraft shuttle between Norrköpings flygplats (ESSP/Kungsängen) and the former Bråvalla F13 airfield.

## Start

Needs Go 1.24 or later and nothing else: the map data is embedded in the binary.

```powershell
cd C:\Users\amarg\dev\norrkoping-simulator-backend
go run ./cmd/simulator                      # serve on 127.0.0.1:8080
go build -o simulator.exe ./cmd/simulator   # one self-contained binary, about 56 MB
```

The server binds to 127.0.0.1:8080. Start the UI with `npm run dev` from the companion repository and open http://127.0.0.1:5173. Restart the server after updating code or the bundled transport data.

| Path | Content |
| --- | --- |
| `cmd/simulator` | Start-up: environment, the two simulation loops, HTTP server, shutdown on Ctrl+C |
| `internal/sim` | The simulation: road, rail and air traffic, terrain, the legacy drone world |
| `internal/api` | Routes, strict JSON command bodies, the Origin and Content-Type guard, SSE streams |
| `src/main/resources` | The bundled map data, written by the UI repository's importers, and `embed.go` that embeds it |
| `src/test/resources` | Control files written by the UI repository's tests |
| `testdata/golden.json` | Frozen reference traces from the earlier Kotlin implementation |

## Kotlin heritage

The server was written in Kotlin/Ktor and ported to Go. The port reproduces the Kotlin implementation's
results, not just its interface. `testdata/golden.json` holds traces written by Kotlin: random number
sequences, city snapshots over 6 000 simulated seconds, a drone scenario and the Step 6.1b bridge
traffic trace (201 516 edge transitions). `go test` replays the same scenarios and compares every value.

The reference is frozen. A deliberate change to the simulation will make `internal/sim/golden_test.go`
fail; the reference then has to be regenerated from the Go implementation, or the comparison retired.
The Kotlin sources, including `GoldenDumpTest.kt` that wrote the reference, are in commit `6636e14`.

`internal/sim/compat.go` holds what the exact reproduction depends on: Kotlin's random generator, Java's
priority queue order and Java's number formatting. Three Kotlin test classes were not ported:
`BusReachabilityTest`, `Step61bTrafficEvidenceTest` and `Step62RoutingAuditTest`.

## CQRS and streaming

```text
POST /api/simulator/* → guard / strict decoding / validation
                     → city command → CityService mutex
                     → RoadTraffic + RailTraffic + AirTraffic (or RegionalFlight) write models
                     → immutable CitySnapshot → State
GET world / SSE city ← read projection
```

Simulation publishes at approximately 10 Hz. Each update advances 0.1 simulation seconds multiplied by the selected time factor, subdividing into steps of at most 0.05 seconds. Under overload simulated time can lag wall time. Pausing freezes movement/time while snapshots remain live. Commands publish immediately after application. Network writes do not hold the simulation mutex.

This is in-memory CQRS. There is no durable event store or database. Restart resets state. The activity list keeps 25 events. SSE sends full snapshots, conflates slow consumers and reconnects with the latest state; event IDs are sequence numbers, not replay cursors. Older `/api/world`, `/api/sse/*` and `/api/commands/*` drone endpoints remain available for compatibility. The current UI uses only `/api/simulator/*`.

## API

All POST requests require `Content-Type: application/json`.

| Method/path | Body / response |
| --- | --- |
| GET `/health` | `{"status":"ok"}` |
| GET `/api/simulator/world` | Current `CitySnapshot` |
| GET `/api/simulator/stream` | SSE event `city`, full snapshot, about 10 Hz |
| POST `/api/simulator/settings` | `{"compact":100,"estate":60,"trucks":20,"buses":12,"trams":8,"trains":2,"flights":true,"planes":3,"timeScale":3}`; `planes` is 1–6, and leaving it out keeps the single legacy shuttle |
| POST `/api/simulator/pause` | `{"paused":true}` |
| POST `/api/simulator/reset` | `{}`; preserve selected settings, reset movement/time |

Limits: compact/estate each 0–600, trucks 0–150, buses 0–80, trams 0–40, trains 0–12, road total at most 1 000, timeScale 1–20. Invalid input returns 400 without changing the projection. Disallowed Origin returns 403; non-JSON writes return 415. Successful commands return a message.

Snapshot fields: `sequence`, `simulationTime`, `paused`, `settings`, `vehicles`, `aircraft`, `completedFlights`, `events`. Vehicles contain ID, kind, position, heading, speed, road and stopped state. Rail vehicles also include articulated `sections` (position/heading per car), and public transport may expose a `stop` name during a dwell. Aircraft additionally contains pitch, bank, phase, origin, destination and gear state. Positions are `[x,y,z]`, Y up, one scene unit = 25 metres. Speeds are km/h; angles radians; time seconds.

## Road traffic

The bundled OSM graph has 38 294 nodes and 73 332 directed edges. Actual shared node IDs determine connectivity, so geometrically crossing roads do not automatically connect. One-way roads and roundabouts are directed. Pedestrian paths, Drottninggatan, restricted/private roads and runways are excluded. Trucks use motorway/trunk/primary/secondary classes and their links, excluding mapped HGV prohibitions.

Vehicles choose routes at graph junctions, accelerate and brake, maintain gaps on upcoming route edges, stop for red lights and reserve junctions. Right-hand lane offsets are applied. Signal programs repeat every 48 simulated seconds with directional green, amber and clearance intervals. Their locations are mapped; timings and priorities are illustrative.

This is a visual traffic simulation, not a complete traffic engineering model. Turn-restriction relations, multi-lane merging, lane changes, pedestrian phases and full vehicle dynamics are not implemented. Road availability/speed defaults depend on map completeness. Existing simplified bridge elevations do not model terrain.

## Public transport

Buses share the road occupancy model and dwell at nearby mapped bus stops. They use generated routes, not scheduled bus services. Five-section yellow/green trams follow OSM line 2/3 relations and stop positions. Four-section trains follow connected main railway tracks west of Norrköping Central, using a simulated return circuit and a white/orange/red Östgötapendeln-inspired model in the UI. Initial counts are 12 buses, 8 trams and 2 trains; legacy settings requests without these fields select zero.

RailTraffic maintains distance on each route, accelerates, brakes and dwells. Each articulated section samples the route independently. Different tram lines and rail/road crossings do not yet share full signal/interlocking control. Stop times and train circulation are illustrative, not live timetables.

The map importer retains tunnel-tagged road underpasses, including Ståthögavägen. Shared signal orientation prevents disagreement between the visible lamps and simulation. Route lookahead cannot loop behind a vehicle; junction leases expire and directed dead ends are pruned before route selection.

## Air traffic

`internal/sim/airtraffic.go` flies one to six aircraft (NK-01 … NK-06) ESSP ↔ F13;
a new city starts with three. Each airport has one runway, used in one direction, and a one-way loop of mapped
taxiways at its take-off end (OpenStreetMap ways 101516378 at ESSP; 104336417 and 104336403 at F13, chosen
because they keep clear of the mapped buildings). A landed aircraft turns on the runway, taxis back to the
loop, goes round it and queues behind the hold line; the first in the queue lines up and takes off.

- **Phases:** `PARKED`, `QUEUE`, `TAXI_OUT`, `TAKEOFF`, `CLIMB`, `CRUISE`, `HOLDING`, `APPROACH`, `LANDING`,
  `TAXI_IN`. `QUEUE` is ready and waiting for the runway; `HOLDING` is a circle at the approach fix, flown
  only if the runway is not free in time, which the clearance rules prevent in normal running.
- **Runway:** one user at a time, and free 20 s before every touchdown. A departure is cleared only if it is
  airborne in time for the next arrival, and if its own touchdown at the other airport leaves room there
  for the arrival before it to vacate and one departure to leave.
- **Timetable:** with five or six aircraft, departures leave in slots, one per 176 s from each airport,
  placed so that the two runways stay in step. Without it six aircraft completed fewer flights than five.
  Up to four aircraft leave as soon as they may.
- **Levels:** flights from ESSP cruise at 12 units (300 m), flights from F13 at 8 (200 m), because the
  tracks of the two directions cross. Every flight ends with a 5° approach from 6 units (150 m).
- **Routes:** three per direction (`airVias`): "över staden", "norr om staden" and "söder om staden". One
  is drawn at random when a flight is cleared, and its name is sent as `route`. A route is turns of 500 m
  radius and straights through its points. All routes of a direction take the same time: a longer one is
  flown faster (2.55 to 3.3 units/s), so the runway rules and the timetable need not know the route.
- **Inside the map:** the network's `meta.clip` is the outline of the mapped area. Every route, climb,
  approach and holding circle is checked against it when the traffic is created, with a margin of 2 units
  (50 m); a route that left the map would be an error, not a flight.
- **Types:** each aircraft is of a type drawn at random when it appears (`airKinds`), sent as `type`:
  `TP` (the unnamed turboprop), `A20N`, `A21N`, `B38M`. The type decides nothing but the picture; the
  queue leaves 60 m between noses for the longest, and at F13 the path bends up to 8 m off the mapped centre
  line beside two buildings so that the widest wing passes (`airObstacles`).
- **Seed:** types and routes come from a seed. A city draws a new one from the clock when it is created or
  reset; the tests pass their own.
- **Changing the number:** the highest numbers leave at once; new aircraft appear at the back of the shorter
  queue.

Every speed, time, distance and level is an estimate chosen for the 1:25 scene; the constants at the top of
the file say what each stands for. `go test` runs every fleet size for six simulated hours and checks
separation, runway use, that nobody waits for ever, that no aircraft ever leaves the map and that every
route is flown.

### The single legacy shuttle

A settings command without `planes` (older clients, and the Kotlin reference traces) flies
`internal/sim/flight.go` as before: one aircraft, NK-01, through `PARKED`, `TAXI_OUT`, `TAKEOFF`, `CLIMB`,
`CRUISE`, `APPROACH`, `LANDING`, `TAXI_IN`. Its trajectory is fixed by `testdata/golden.json`.

F13 service is fictional. Flight paths, speeds and phases are simplified geometric animation, not actual aviation procedures or a flight-dynamics model. No live aircraft/traffic feed is used.

## Map data

`src/main/resources/transport-network.json` is derived from © OpenStreetMap contributors under ODbL 1.0. See DATA-LICENSE.md. The UI importer creates both render geometry and this transport graph from one snapshot. To update both together, run from the UI repository:

```powershell
npm run map:import -- C:\path\norrkoping-region.json ..\norrkoping-simulator-backend\src\main\resources\transport-network.json
```

The source query, timestamp, geographic bounds and scale are recorded in `meta`. No external map request is made at application runtime.

## Configuration and tests

| Variable | Default | Purpose |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | Listen address |
| `PORT` | `8080` | Listen port |
| `ALLOWED_ORIGIN` | `http://localhost:5173` | Browser origins that may send commands and, from another domain, read answers and streams (CORS); several separated by commas. 127.0.0.1:5173 is also accepted |
| `MAX_CITIES` | `20` | Visitors' own cities at the same time; the next visitor gets 503 |
| `CITY_IDLE` | `60` | Seconds without a listener or command before a visitor's city is removed |

```powershell
go test ./...    # about 5 seconds
go vet ./...
```

Tests cover the Kotlin reference traces, traffic counts/restrictions, red-light stopping and green release, continuous flight through all phases and both airports, the fleet of one to six aircraft (separation, one runway user at a time, no deadlock, a changed fleet size, holding), pause, invalid commands without mutation, HTTP validation and origin checks. Legacy drone tests remain. Browser/SSE end-to-end tests are in the UI repo.

Every `/api/simulator/*` route takes an optional `?session=<id>` (8–64 characters of `A–Z a–z 0–9 _ -`): the request then uses a city of that visitor's own, created on first use and removed when idle. Without the parameter everybody shares one city.

Container and cluster: `Dockerfile` builds one static binary (`HOST=0.0.0.0`); `.github/workflows/image.yml` publishes it to ghcr.io on every push to `main`; `deploy/k8s/simulator.yaml` is the k3s deployment (one replica, Traefik ingress, limits per address). The steps are in the UI repository, `docs/deploy/README.md`.

Application without authentication or persistence. For an isolated verification instance use `PORT=8081`, `ALLOWED_ORIGIN=http://127.0.0.1:5174`, and point the test UI's `API_TARGET` to port 8081.

## Shared elevation contract

TerrainService reads the same terrain.json as the UI. Road and rail projections use its common height profile; snapshots identify the terrain revision. The bundled dataset is currently empty (unavailable), so no surveyed topography is claimed. See ../norrkoping-simulator-ui/ELEVATION.md for data selection, import contracts, and remaining GIS/special-geometry migration work.
