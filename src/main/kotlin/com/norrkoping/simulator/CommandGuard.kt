package com.norrkoping.simulator

import io.ktor.http.*
import io.ktor.server.application.*
import io.ktor.server.request.*
import io.ktor.server.response.*

/** Same-origin browser commands; CLI clients without Origin are supported on the loopback server. */
val CommandGuard = createRouteScopedPlugin("CommandGuard") {
    onCall { call ->
        if (call.request.httpMethod == HttpMethod.Post) {
            val origin = call.request.header(HttpHeaders.Origin)
            val allowed = System.getenv("ALLOWED_ORIGIN") ?: "http://localhost:5173"
            if (origin != null && origin != allowed && origin != "http://127.0.0.1:5173") {
                call.respond(HttpStatusCode.Forbidden, ApiError("Origin is not allowed."))
            } else if (call.request.contentType().withoutParameters() != ContentType.Application.Json) {
                call.respond(HttpStatusCode.UnsupportedMediaType, ApiError("Use application/json."))
            }
        }
    }
}