package com.norrkoping.simulator

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import kotlin.random.Random
import kotlin.test.Test
import java.io.File

/**
 * Writes testdata/golden.json: deterministic reference traces of this Kotlin implementation, which the
 * Go port must reproduce (internal/sim/golden_test.go). Runs only with GOLDEN_DUMP=1:
 *
 *   $env:GOLDEN_DUMP = '1'; .\gradlew.bat test --tests '*GoldenDumpTest*'
 */
class GoldenDumpTest {
    private val codec = Json { encodeDefaults = true }

    private fun randomTrace(seed: Int) = buildJsonObject {
        val r = Random(seed)
        put("ints", JsonArray(List(20) { JsonPrimitive(r.nextInt()) }))
        put("doubles", JsonArray(List(20) { JsonPrimitive(r.nextDouble()) }))
        put("bounded7", JsonArray(List(20) { JsonPrimitive(r.nextInt(7)) }))
        put("bounded64", JsonArray(List(20) { JsonPrimitive(r.nextInt(64)) }))
        put("bounded23", JsonArray(List(20) { JsonPrimitive(r.nextInt(23)) }))
        put("ranged", JsonArray(List(20) { JsonPrimitive(r.nextDouble(-1.2, 1.2)) }))
    }

    private suspend fun city() = buildJsonObject {
        val city = CitySimulationService()
        city.execute(CityCommand.Configure(TrafficSettings(compact = 12, estate = 6, trucks = 4, flights = true, timeScale = 20.0, buses = 5, trams = 3, trains = 2)))
        val marks = setOf(1, 10, 100, 500, 1000, 2000, 3000)
        put("run", buildJsonArray {
            for (i in 1..3000) {
                city.step(.1)
                if (i in marks) add(buildJsonObject { put("step", i); put("snapshot", codec.encodeToJsonElement(city.world.value)) })
            }
        })
        city.execute(CityCommand.Pause(true))
        city.step(.1)
        put("paused", codec.encodeToJsonElement(city.world.value))
        city.execute(CityCommand.Reset)
        city.execute(CityCommand.Configure(TrafficSettings(compact = 3, estate = 2, trucks = 1, flights = false, timeScale = 1.0, buses = 1, trams = 1, trains = 1)))
        repeat(50) { city.step(.1) }
        put("afterReset", codec.encodeToJsonElement(city.world.value))
        val failure = runCatching { city.execute(CityCommand.Configure(TrafficSettings(600, 600, 150))) }.exceptionOrNull()
        put("invalid", failure?.message)
    }

    private suspend fun drones() = buildJsonArray {
        val service = SimulationService()
        var frame = 0L
        suspend fun advance(to: Long) { while (frame < to) service.step(0.05, ++frame) }
        suspend fun mark(label: String, command: Command? = null) {
            val error = command?.let { runCatching { service.execute(it) }.exceptionOrNull()?.message }
            add(buildJsonObject {
                put("label", label); put("frame", frame); put("error", error)
                put("world", codec.encodeToJsonElement(service.world.value))
                put("telemetry", codec.encodeToJsonElement(service.telemetry.value))
            })
        }
        mark("initial")
        advance(100); mark("zone", Command.AddZone(ZoneRequest(listOf(0.0, 15.0, 0.0))))
        advance(120); mark("zone2", Command.AddZone(ZoneRequest(listOf(20.0, 18.0, -20.0), 9.5)))
        advance(200); mark("wind", Command.Wind(WindRequest(3.5, -2.25)))
        advance(300); mark("dispatch", Command.Dispatch(DispatchRequest(count = 3, originHub = "HUB_VRINNEVI", weightKg = 1.5, destination = "arbetets museum")))
        mark("unknown", Command.Dispatch(DispatchRequest(destination = "Stockholm")))
        advance(1000); mark("cruise")
        mark("removeZone", Command.RemoveZone("Z-1"))
        advance(1500); mark("recall", Command.Recall)
        advance(2400); mark("home")
        mark("reset", Command.Reset)
        advance(2500); mark("restarted")
    }

    private fun trace() = buildJsonObject {
        val accepted = { t: TrafficTrace ->
            t.crossings("bus").isNotEmpty() && t.dwells.any { it.stop == "Skvallertorget" && it.kind == "bus" } &&
                t.carPassages().size >= Step61bTrafficRegressionTest.CAR_PASSAGES && t.crossings("car").isNotEmpty()
        }
        val t = TrafficTrace().run(Step61bTrafficRegressionTest.SCENARIO, Step61bTrafficRegressionTest.LIMIT_SECONDS, accepted)
        put("time", t.time); put("transitions", t.transitions); put("hiddenFromSampling", t.hiddenFromSampling)
        put("violations", t.violations.size)
        put("reversals", JsonArray(t.reversals.map { JsonPrimitive(t.describe(it)) }))
        put("episodes", JsonArray(t.episodes.map { JsonPrimitive(t.describe(it)) }))
        put("dwells", JsonArray(t.dwells.map { JsonPrimitive("${it.kind} #${it.vehicle} ${it.stop} ${it.time}") }))
        put("gangfartEntries", JsonObject(t.gangfartEntries.toSortedMap().mapValues { JsonPrimitive(it.value) }))
        put("allowed", JsonObject(t.allowed.mapValues { JsonPrimitive(it.value.size) }))
        put("allowedChecksum", JsonObject(t.allowed.mapValues { JsonPrimitive(it.value.fold(0L) { a, e -> (a * 31 + e) % 1000000007L }) }))
    }

    @Test fun dump() {
        if (System.getenv("GOLDEN_DUMP") != "1") return
        val golden = runBlocking {
            buildJsonObject {
                put("random", buildJsonObject { put("1309", randomTrace(1309)); put("42", randomTrace(42)); put("-7", randomTrace(-7)) })
                put("city", city())
                put("drones", drones())
                put("trace", trace())
            }
        }
        File("testdata").mkdirs()
        File("testdata/golden.json").writeText(codec.encodeToString(JsonElement.serializer(), golden))
    }
}
