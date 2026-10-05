package eu.kanade.tachiyomi.network.interceptor

/*
 * This Source Code Form is subject to the terms of the Mozilla Public License, v. 2.0.
 * If a copy of the MPL was not distributed with this file, obtain one at https://mozilla.org/MPL/2.0/.
 */

import kotlinx.coroutines.delay
import kotlinx.serialization.json.*
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Call
import java.io.IOException
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.Semaphore
import java.util.concurrent.TimeUnit

/** Owns nested transports until their complete response, independently of an app caller. */
internal object SolverTransport {
    private class Lease(var busy: Boolean = false, var unresolved: Boolean = false, var users: Int = 0, var generation: String? = null, var capabilityChecked: Boolean = false)
    private val leases = mutableMapOf<Pair<String, String>, Lease>()
    private val callers = Semaphore(128)
    private val active = Semaphore(8)
    private val executor = Executors.newFixedThreadPool(8) { task ->
        Thread(task, "solver-transport").apply { isDaemon = true }
    }

    suspend fun <T> response(
        outer: Call,
        nested: Call,
        client: OkHttpClient,
        endpoint: String,
        session: String,
        budgetMillis: Long,
        parse: (String) -> T,
    ): T {
        if (!callers.tryAcquire()) throw IOException("Solver waiting capacity exhausted")
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(budgetMillis)
        val key = endpoint to session
        var lease: Lease? = null
        var leaseHeld = false
        var permitHeld = false
        var submitted = false
        fun checkCaller() {
            if (outer.isCanceled()) throw IOException("Source request cancelled")
            if (System.nanoTime() >= deadline) throw IOException("Solver challenge deadline exceeded")
        }
        try {
            checkCaller()
            if (session.isNotEmpty()) {
                lease = synchronized(leases) {
                    val existing = leases[key]
                    if (existing == null && leases.size >= 128) throw IOException("Solver session capacity exhausted")
                    (existing ?: Lease().also { leases[key] = it }).also { it.users++ }
                }
                while (!leaseHeld) {
                    checkCaller()
                    leaseHeld = synchronized(leases) {
                        if (lease!!.unresolved && lease!!.generation == null) throw IOException("Solver session unresolved after transport failure; remote recovery required")
                        if (lease!!.busy) false else { lease!!.busy = true; true }
                    }
                    if (!leaseHeld) delay(20)
                }
            }
            while (!permitHeld) {
                checkCaller()
                permitHeld = active.tryAcquire()
                if (!permitHeld) delay(20)
            }
            checkCaller()
            val result = CompletableFuture<T>()
            val ownedLease = lease
            executor.execute {
                var terminal = false
                try {
                    // Cancellation before registration must not start a remote browser.
                    if (outer.isCanceled() || System.nanoTime() >= deadline) {
                        terminal = true
                        throw IOException("Source request cancelled or expired before solver transport")
                    }
                    // A named solve drains under its own bounded clock; lease waiting only
                    // consumes the caller budget, not the admitted browser transport budget.
                    val transportDeadline = if (session.isEmpty()) deadline else
                        System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(budgetMillis)
                    var solve = nested
                    if (ownedLease != null) {
                        if (ownedLease.unresolved) {
                            val previous = ownedLease.generation ?: throw IOException("Solver lacks confirmed recovery")
                            val acknowledgement = control(client, endpoint, session, transportDeadline, "sessions.recovery.confirm", previous)
                            ownedLease.generation = verifyAcknowledgement(acknowledgement, session, "drained-closed", previous)
                            ownedLease.unresolved = false
                        } else if (!ownedLease.capabilityChecked) {
                            // Discovery cannot acquire a browser or enable a session fence.
                            terminal = true
                            val capability = client.newCall(Request.Builder().url("$endpoint/v1/session-recovery").get().build())
                            capability.timeout().deadlineNanoTime(transportDeadline)
                            capability.execute().use { response ->
                                val body = response.body.string()
                                if (response.code != 404) {
                                    if (!response.isSuccessful || (Json.parseToJsonElement(body).jsonObject["protocol"] as? JsonPrimitive)?.let { it.isString && it.content == "fenced-drain-close-v1" } != true) {
                                        throw IOException("Solver recovery capability was not verified")
                                    }
                                    terminal = false
                                    val prepared = control(client, endpoint, session, transportDeadline, "sessions.recovery.prepare", null)
                                    ownedLease.generation = verifyAcknowledgement(prepared, session, "prepared", null)
                                }
                                ownedLease.capabilityChecked = true
                            }
                        }
                        if (outer.isCanceled() || System.nanoTime() >= deadline) {
                            terminal = true
                            throw IOException("Source request cancelled or expired before browser solve")
                        }
                        ownedLease.generation?.let { generation ->
                            solve = client.newCall(nested.request().newBuilder().header("X-Byparr-Session-Generation", generation).build())
                        }
                    }
                    val solveDeadline = if (session.isEmpty()) deadline else
                        System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(budgetMillis)
                    solve.timeout().deadlineNanoTime(solveDeadline)
                    terminal = false
                    solve.execute().use { response ->
                        val body = response.body.string()
                        if (!response.isSuccessful) throw IOException("Solver HTTP ${response.code}")
                        val parsed = parse(body)
                        terminal = true
                        result.complete(parsed)
                    }
                } catch (error: Throwable) {
                    result.completeExceptionally(error)
                } finally {
                    synchronized(leases) {
                        if (ownedLease != null) {
                            if (!terminal) ownedLease.unresolved = true
                            ownedLease.busy = false
                            if (ownedLease.users == 0 && !ownedLease.unresolved && ownedLease.generation == null) leases.remove(key)
                        }
                    }
                    active.release()
                }
            }
            submitted = true
            while (!result.isDone) {
                checkCaller()
                delay(20)
            }
            checkCaller()
            return try { result.get() } catch (error: ExecutionException) {
                throw IOException("Solver transport failed", error.cause)
            }
        } finally {
            if (submitted) {
                // A named browser lease remains owned by the transport until its terminal body.
                if (session.isEmpty()) nested.cancel()
            } else {
                nested.cancel()
                if (permitHeld) active.release()
            }
            synchronized(leases) {
                lease?.let {
                    if (!submitted && leaseHeld) it.busy = false
                    it.users--
                    if (it.users == 0 && !it.busy && !it.unresolved && it.generation == null) leases.remove(key)
                }
            }
            callers.release()
        }
    }

    private fun control(client: OkHttpClient, endpoint: String, session: String, deadline: Long, command: String, generation: String?): JsonObject {
        val payload = buildJsonObject {
            put("cmd", command)
            put("session", session)
            generation?.let { put("sessionGeneration", it) }
        }
        val call = client.newCall(Request.Builder().url("$endpoint/v1").post(payload.toString().toRequestBody("application/json".toMediaType())).build())
        call.timeout().deadlineNanoTime(deadline)
        return call.execute().use { response ->
            val body = response.body.string()
            if (!response.isSuccessful) throw IOException("Solver confirmed recovery HTTP ${response.code}")
            Json.parseToJsonElement(body).jsonObject
        }
    }

    private fun verifyAcknowledgement(body: JsonObject, session: String, outcome: String, previous: String?): String {
        fun value(key: String): String? {
            val primitive = body[key] as? JsonPrimitive ?: return null
            return if (primitive.isString) primitive.content else null
        }
        val generation = value("generation")
        if (value("protocol") != "fenced-drain-close-v1" || value("session") != session || value("outcome") != outcome ||
            generation == null || !generation.matches(Regex("[0-9a-f]{32}")) ||
            (previous != null && (value("previousGeneration") != previous || generation == previous)) ||
            (previous == null && body["previousGeneration"] != null && body["previousGeneration"] != JsonNull)) {
            throw IOException("Solver confirmed recovery acknowledgement mismatch")
        }
        return generation
    }

}
