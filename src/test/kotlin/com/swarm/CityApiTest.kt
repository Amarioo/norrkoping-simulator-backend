package com.swarm

import io.ktor.client.request.*
import io.ktor.client.statement.*
import io.ktor.http.*
import io.ktor.server.testing.*
import kotlinx.serialization.json.*
import kotlin.test.*

class CityApiTest {
    @Test fun `simulator settings update the read model and reject invalid commands`() = testApplication {
        application { module() }
        val result=client.post("/api/simulator/settings") {
            contentType(ContentType.Application.Json)
            setBody("""{"compact":7,"estate":3,"trucks":2,"flights":false,"timeScale":1}""")
        }
        assertEquals(HttpStatusCode.OK,result.status)
        val world=Json.parseToJsonElement(client.get("/api/simulator/world").bodyAsText()).jsonObject
        assertEquals(12,world.getValue("vehicles").jsonArray.size)
        assertEquals(0,world.getValue("aircraft").jsonArray.size)
        assertEquals(HttpStatusCode.BadRequest,client.post("/api/simulator/settings") {
            contentType(ContentType.Application.Json)
            setBody("""{"compact":600,"estate":600,"trucks":150}""")
        }.status)
        assertEquals(HttpStatusCode.BadRequest,client.post("/api/simulator/settings") {
            contentType(ContentType.Application.Json);setBody("{")
        }.status)
        assertEquals(HttpStatusCode.Forbidden,client.post("/api/simulator/reset") {
            header(HttpHeaders.Origin,"https://unrelated.example")
            contentType(ContentType.Application.Json);setBody("{}")
        }.status)
        assertEquals(HttpStatusCode.OK,client.post("/api/simulator/pause") {
            contentType(ContentType.Application.Json);setBody("""{"paused":true}""")
        }.status)
        val paused=Json.parseToJsonElement(client.get("/api/simulator/world").bodyAsText()).jsonObject
        assertTrue(paused.getValue("paused").jsonPrimitive.boolean)
        assertEquals(12,paused.getValue("vehicles").jsonArray.size)
        assertEquals(HttpStatusCode.OK,client.post("/api/simulator/reset") {
            contentType(ContentType.Application.Json);setBody("{}")
        }.status)
    }
}
