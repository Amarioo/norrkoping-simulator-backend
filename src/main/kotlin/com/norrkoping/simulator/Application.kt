package com.norrkoping.simulator

import io.ktor.http.*
import io.ktor.serialization.kotlinx.json.*
import io.ktor.server.application.*
import io.ktor.server.engine.*
import io.ktor.server.netty.*
import io.ktor.server.plugins.*
import io.ktor.server.plugins.contentnegotiation.*
import io.ktor.server.plugins.statuspages.*
import io.ktor.server.request.*
import io.ktor.server.response.*
import io.ktor.server.routing.*
import io.ktor.server.sse.*
import io.ktor.sse.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json

fun main() {
    val port = System.getenv("PORT")?.toIntOrNull() ?: 8080
    embeddedServer(Netty, host = System.getenv("HOST") ?: "127.0.0.1", port = port, module = Application::module).start(wait = true)
}
fun Application.module() {
    val jsonCodec = Json { encodeDefaults = true }
    install(ContentNegotiation) { json(jsonCodec) }
    install(SSE)
    install(StatusPages) {
        exception<IllegalArgumentException> { call, cause -> call.respond(HttpStatusCode.BadRequest, ApiError(cause.message ?: "Invalid command.")) }
        exception<BadRequestException> { call, _ -> call.respond(HttpStatusCode.BadRequest, ApiError("Malformed command body.")) }
    }
    val city = CitySimulationService()
    val cityJob = launch(Dispatchers.Default) { city.run() }
    monitor.subscribe(ApplicationStopping) { cityJob.cancel() }
    val simulation = SimulationService()
    val job = launch(Dispatchers.Default) { simulation.run() }
    monitor.subscribe(ApplicationStopping) { job.cancel() }
    routing {
        get("/health") { call.respond(mapOf("status" to "ok")) }
        route("/api") {
            install(CommandGuard)
            route("/simulator") {
                get("/world") { call.respond(city.world.value) }
                post("/settings") { city.execute(CityCommand.Configure(call.receive())); call.respond(CommandResult("Trafiken uppdaterad.")) }
                post("/pause") { city.execute(CityCommand.Pause(call.receive<PauseRequest>().paused)); call.respond(CommandResult("Paus uppdaterad.")) }
                post("/reset") { city.execute(CityCommand.Reset); call.respond(CommandResult("Simuleringen återställd.")) }
                sse("/stream") { city.world.collect { send(ServerSentEvent(data=jsonCodec.encodeToString(it),event="city",id=it.sequence.toString(),retry=1500)) } }
            }
            get("/world") { call.respond(simulation.world.value) }
            get("/telemetry") { call.respond(simulation.telemetry.value) }
            post("/commands/dispatch-drone") { simulation.execute(Command.Dispatch(call.receive())); call.respond(CommandResult("Drones dispatched.")) }
            post("/commands/spawn-no-fly-zone") { simulation.execute(Command.AddZone(call.receive())); call.respond(CommandResult("Zone activated.")) }
            post("/commands/remove-no-fly-zone") { simulation.execute(Command.RemoveZone(call.receive<IdRequest>().id)); call.respond(CommandResult("Zone removed.")) }
            post("/commands/pause") { simulation.execute(Command.Pause(call.receive<PauseRequest>().paused)); call.respond(CommandResult("Pause updated.")) }
            post("/commands/wind") { simulation.execute(Command.Wind(call.receive())); call.respond(CommandResult("Wind updated.")) }
            post("/commands/recall") { simulation.execute(Command.Recall); call.respond(CommandResult("Fleet recalled.")) }
            post("/commands/reset") { simulation.execute(Command.Reset); call.respond(CommandResult("Simulation reset.")) }
            sse("/sse/world") {
                simulation.world.collect { send(ServerSentEvent(data = jsonCodec.encodeToString(it), event = "world", id = it.sequence.toString(), retry = 1500)) }
            }
            sse("/sse/telemetry") {
                simulation.telemetry.collect { send(ServerSentEvent(data = jsonCodec.encodeToString(it), event = "telemetry", id = it.sequence.toString(), retry = 1500)) }
            }
        }
    }
}