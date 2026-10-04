package eu.kanade.tachiyomi.network.interceptor

/*
 * This Source Code Form is subject to the terms of the Mozilla Public License, v. 2.0.
 * If a copy of the MPL was not distributed with this file, obtain one at https://mozilla.org/MPL/2.0/.
 */

import kotlinx.coroutines.delay
import okhttp3.Call
import java.io.IOException
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.Semaphore
import java.util.concurrent.TimeUnit

/** Owns nested transports until their complete response, independently of an app caller. */
internal object SolverTransport {
    private class Lease(var busy: Boolean = false, var unresolved: Boolean = false, var users: Int = 0)
    private val leases = mutableMapOf<Pair<String, String>, Lease>()
    private val callers = Semaphore(128)
    private val active = Semaphore(8)
    private val executor = Executors.newFixedThreadPool(8) { task ->
        Thread(task, "solver-transport").apply { isDaemon = true }
    }

    suspend fun <T> response(
        outer: Call,
        nested: Call,
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
                        if (lease!!.unresolved) throw IOException("Solver session unresolved after transport failure; remote recovery required")
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
            nested.timeout().deadlineNanoTime(deadline)
            val result = CompletableFuture<T>()
            val ownedLease = lease
            executor.execute {
                var terminal = false
                try {
                    // Cancellation before registration must not start a remote browser.
                    if (outer.isCanceled()) {
                        terminal = true
                        throw IOException("Source request cancelled before solver transport")
                    }
                    nested.execute().use { response ->
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
                            if (ownedLease.users == 0 && !ownedLease.unresolved) leases.remove(key)
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
                    if (it.users == 0 && !it.busy && !it.unresolved) leases.remove(key)
                }
            }
            callers.release()
        }
    }
}
