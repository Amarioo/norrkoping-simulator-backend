# Norrköping Simulator — Kotlin/Ktor

A local city simulation for the Norrköping Simulator UI in `../drone-swarm-ui`. Cars use a directed OpenStreetMap road graph. A fictional regional aircraft travels between Norrköpings flygplats (ESSP/Kungsängen) and the former Bråvalla F13 airfield.

## Start

```powershell
cd C:\Users\amarg\dev\drone-swarm-backend
.\dev.ps1
```

Requires JDK 21 or 25. `dev.ps1` discovers an installed JDK under `%USERPROFILE%\.jdks` when `JAVA_HOME` is unset. Gradle is included through its wrapper. The server binds to 127.0.0.1:8080. Start the UI with `npm run dev` from the companion repository and open http://127.0.0.1:5173.

The Gradle project is named `norrkoping-simulator-backend`; the existing folder name is retained so editor/start configurations keep working. Restart Ktor after updating code or the bundled transport data.

## CQRS and streaming

```text
POST /api/simulator/* → CommandGuard / validation
                     → CityCommand → CitySimulationService mutex
                     → RoadTraffic + RegionalFlight write models
                     → immutable CitySnapshot → StateFlow
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
| POST `/api/simulator/settings` | `{"compact":100,"estate":60,"trucks":20,"flights":true,"timeScale":3}` |
| POST `/api/simulator/pause` | `{"paused":true}` |
| POST `/api/simulator/reset` | `{}`; preserve selected settings, reset movement/time |

Limits: compact/estate each 0–600, trucks 0–150, total at most 1 000, timeScale 1–20. Invalid input returns 400 without changing the projection. Disallowed Origin returns 403; non-JSON writes return 415. Successful commands return a message.

Snapshot fields: `sequence`, `simulationTime`, `paused`, `settings`, `vehicles`, `aircraft`, `completedFlights`, `events`. Vehicles contain ID, kind, position, heading, speed, road and stopped state. Aircraft additionally contains pitch, bank, phase, origin, destination and gear state. Positions are `[x,y,z]`, Y up, one scene unit = 25 metres. Speeds are km/h; angles radians; time seconds.

## Road traffic

The bundled OSM graph has 38 294 nodes and 73 315 directed edges. Actual shared node IDs determine connectivity, so geometrically crossing roads do not automatically connect. One-way roads and roundabouts are directed. Pedestrian paths, Drottninggatan, restricted/private roads and runways are excluded. Trucks use motorway/trunk/primary/secondary classes and their links, excluding mapped HGV prohibitions.

Vehicles choose routes at graph junctions, accelerate and brake, maintain gaps on upcoming route edges, stop for red lights and reserve junctions. Right-hand lane offsets are applied. Signal programs repeat every 48 simulated seconds with directional green, amber and clearance intervals. Their locations are mapped; timings and priorities are illustrative.

This is a visual traffic simulation, not a complete traffic engineering model. Turn-restriction relations, multi-lane merging, lane changes, pedestrian phases and full vehicle dynamics are not implemented. Road availability/speed defaults depend on map completeness. Existing simplified bridge elevations do not model terrain.

## Regional flight

One procedural twin-engine turboprop shuttles ESSP ↔ F13. Runway endpoints/headings and taxiway lines come from the same map as the renderer. The flight builds connected taxi routes, then cycles through `PARKED`, `TAXI_OUT`, `TAKEOFF`, `CLIMB`, `CRUISE`, `APPROACH`, `LANDING`, `TAXI_IN`. Ground movement, climb/descent, bank, propellers and landing gear are visualised by the UI.

F13 service is fictional. Flight paths, speeds and phases are simplified geometric animation, not actual aviation procedures or a flight-dynamics model. No live aircraft/traffic feed is used.

## Map data

`src/main/resources/transport-network.json` is derived from © OpenStreetMap contributors under ODbL 1.0. See DATA-LICENSE.md. The UI importer creates both render geometry and this transport graph from one snapshot. To update both together, run from the UI repository:

```powershell
npm run map:import -- C:\path\norrkoping-region.json ..\drone-swarm-backend\src\main\resources\transport-network.json
```

The source query, timestamp, geographic bounds and scale are recorded in `meta`. No external map request is made at application runtime.

## Configuration and tests

| Variable | Default | Purpose |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | Listen address |
| `PORT` | `8080` | Listen port |
| `ALLOWED_ORIGIN` | `http://localhost:5173` | Browser command origin; 127.0.0.1:5173 is also accepted |

```powershell
.\dev.ps1 test
.\dev.ps1 build
```

Tests cover traffic counts/restrictions, red-light stopping and green release, continuous flight through all phases and both airports, pause, invalid commands without mutation, HTTP validation and origin checks. Legacy drone tests remain. Browser/SSE end-to-end tests are in the UI repo.

Local single-user application without authentication or persistence. For an isolated verification instance use `PORT=8081`, `ALLOWED_ORIGIN=http://127.0.0.1:5174`, and point the test UI's `API_TARGET` to port 8081.
