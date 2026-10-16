package com.swarm

import io.ktor.client.request.*
import io.ktor.client.statement.*
import io.ktor.http.*
import io.ktor.server.testing.*
import kotlin.test.*
import kotlinx.serialization.json.*

class ApplicationTest {
    @Test fun `commands update query read model and reject malformed inputs`() = testApplication {
        application { module() }
        assertEquals(HttpStatusCode.OK, client.get("/health").status)
        val accepted = client.post("/api/commands/dispatch-drone") { contentType(ContentType.Application.Json); setBody("""{"count":3,"originHub":"HUB_SOUTH","weightKg":1.5}""") }
        assertEquals(HttpStatusCode.OK, accepted.status)
        val world = Json.parseToJsonElement(client.get("/api/world").bodyAsText()).jsonObject
        assertEquals(11, world.getValue("drones").jsonArray.size)
        val invalid = client.post("/api/commands/spawn-no-fly-zone") { contentType(ContentType.Application.Json); setBody("""{"center":[0,15],"radius":12}""") }
        assertEquals(HttpStatusCode.BadRequest, invalid.status)
        assertTrue(Json.parseToJsonElement(invalid.bodyAsText()).jsonObject.containsKey("error"))
        val malformed = client.post("/api/commands/dispatch-drone") { contentType(ContentType.Application.Json); setBody("{") }
        assertEquals(HttpStatusCode.BadRequest, malformed.status)
    }
    @Test fun `cross origin writes and form posts are rejected`() = testApplication {
        application { module() }
        val crossOrigin = client.post("/api/commands/reset") { header(HttpHeaders.Origin, "https://unrelated.example"); contentType(ContentType.Application.Json); setBody("{}") }
        assertEquals(HttpStatusCode.Forbidden, crossOrigin.status)
        assertEquals(HttpStatusCode.UnsupportedMediaType, client.post("/api/commands/reset") { setBody("reset=true") }.status)
    }
}