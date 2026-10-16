# Swarm / simulation backend

A local 3D drone sandbox with Kotlin 2.4.21, Ktor 3.6.0, CQRS and Server-Sent Events. The companion repository is `../drone-swarm-ui` (Svelte 5 + Threlte).

## Start on Windows

Requires JDK 21 or 25. Gradle 9.3.1 is included through its wrapper.

```powershell
cd C:\Users\amarg\dev\drone-swarm-backend
.\dev.ps1
```

`dev.ps1` uses `JAVA_HOME`, or discovers a compatible JDK under `%USERPROFILE%\.jdks`. It changes only the current process environment. On this machine it finds the installed Microsoft JDK 25. For a different JDK, set `JAVA_HOME` explicitly. Open the project folder directly in IntelliJ IDEA if preferred.

In another terminal:

```powershell
cd C:\Users\amarg\dev\drone-swarm-ui
npm run dev
```

Open http://127.0.0.1:5173. The backend listens on **127.0.0.1:8080**. Stop each terminal with Ctrl+C. For macOS/Linux with Java configured, use `sh gradlew run`.

## Architecture

```text
POST command → CommandGuard / JSON validation
             → SimulationService (Mutex: one writer)
             → SwarmWorld (write aggregate + pathfinder)
             → DomainEvent list
             → Projections (independent immutable read DTOs)
             → StateFlow → GET query / SSE → browser
```

- The fixed simulation step is 50 ms (20 Hz). World snapshots publish every second step (10 Hz); telemetry every tenth step (2 Hz). Commands immediately publish both projections.
- Pausing freezes simulation time and movement while snapshot sequences continue, keeping streams live.
- `StateFlow` retains the newest complete snapshot and conflates slow subscribers. Network writes never run under the simulation mutex. Reconnecting clients receive the current world, not a replay of every missed tick. SSE event IDs are sequence numbers; `Last-Event-ID` does not trigger replay.
- Domain events update the read side. This is **in-memory CQRS, not durable event sourcing**. Restart/reset discards the simulation. The activity projection keeps the most recent 50 entries.
- Backend commands return 200 only after they have been applied and projected, or a JSON error with 400/403/415.

## Simulation

Eight drones start at the north hub. Dispatch up to 20 per command, with a fleet limit of 100. Drones deliver, return, recharge and repeat. Recall disables repeat missions for **the entire fleet**, including drones already at their hub. Reset creates a fresh fleet.

Cruise lanes are at 18, 22 and 26 metres. A bounded A* grid finds horizontal routes around spherical exclusion zones with a 2 m margin. A line-of-sight pass simplifies routes, and each movement segment is checked again. A blocked target yields `HOLDING`. Clearing the blocking zone replans the route. Zones cannot cover a drone's current position or a hub.

This is a visual sandbox: buildings are decorative below the flight lanes, there is no drone-to-drone collision solver, vertical takeoff/landing is not simulated, and wind changes ground speed along the route rather than modelling full aerodynamics. Battery exhaustion is shown as an emergency marker, not physical falling.

## API

All command bodies use `Content-Type: application/json`.

| Method / path | Body or response |
| --- | --- |
| GET `/health` | `{"status":"ok"}` |
| GET `/api/world` | Current world snapshot |
| GET `/api/telemetry` | Current metrics and recent activity |
| GET `/api/sse/world` | Named event `world`, full snapshot, ~10 Hz |
| GET `/api/sse/telemetry` | Named event `telemetry`, full snapshot, ~2 Hz |
| POST `/api/commands/dispatch-drone` | `{"count":5,"originHub":"HUB_NORTH","weightKg":2}` |
| POST `/api/commands/spawn-no-fly-zone` | `{"center":[0,15,0],"radius":12}` |
| POST `/api/commands/remove-no-fly-zone` | `{"id":"Z-1"}` |
| POST `/api/commands/pause` | `{"paused":true}` |
| POST `/api/commands/wind` | `{"x":8,"z":4}` |
| POST `/api/commands/recall` | `{}` |
| POST `/api/commands/reset` | `{}` |

Available hubs: `HUB_NORTH`, `HUB_SOUTH`. Units: metres, seconds, kilograms and battery percent. Coordinates are `[x,y,z]` with Y up. Zone radius: 4–18 m; max 24 zones; center X/Z: ±65 m, Y: 0–35 m. Wind components: ±10 m/s. Payload: 0.1–5 kg.

World fields: `sequence`, `simulationTime`, `paused`, `wind`, `drones`, `noFlyZones`, `hubs`. Drone fields: `id`, `pos`, `target`, `battery`, `state`, `weightKg`, `route`. Telemetry fields: `sequence`, `active`, `delivering`, `returning`, `holding`, `deliveries`, `averageBattery`, `distanceMetres`, `events`.

```powershell
Invoke-RestMethod http://127.0.0.1:8080/api/commands/dispatch-drone -Method Post -ContentType application/json -Body '{"count":5}'
curl.exe -N http://127.0.0.1:8080/api/sse/world
```

## Build and test

```powershell
.\dev.ps1 test
.\dev.ps1 build
```

Tests cover spherical exclusion routing, blocked targets, validation without mutation, concurrent commands, immutable snapshots, pause/reset, completed missions, whole-fleet recall and HTTP validation/origin checks. Browser end-to-end tests live in the UI repo and also verify real Netty SSE streaming.

The build produces an application distribution under `build/distributions/`. GitHub Actions is configured for JDK 25 and the Gradle wrapper.

## Configuration

| Environment | Default | Purpose |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | Listen address |
| `PORT` | `8080` | Backend port |
| `ALLOWED_ORIGIN` | `http://localhost:5173` | Allowed browser command origin; `http://127.0.0.1:5173` is also allowed |

Vite proxies `/api` in development and preview, so CORS is unnecessary. There is no authentication or persistence; the default is a local single-user sandbox. Add authentication and a deployment-specific origin policy before exposing it remotely.

References: [Ktor SSE](https://ktor.io/docs/server-server-sent-events.html), [Threlte useTask](https://threlte.xyz/docs/reference/core/use-task/).