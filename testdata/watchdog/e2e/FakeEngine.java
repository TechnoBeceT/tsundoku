import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * FakeEngine — a stand-in for the engine-host's {@code RpcServer}, used by the wedge-watchdog
 * end-to-end test (GAP-137). It exists so the watchdog can be driven against a REAL JVM producing
 * a REAL SIGQUIT thread dump, instead of against a checked-in text fixture.
 *
 * <p><b>READ THIS BEFORE "SIMPLIFYING" ANYTHING BELOW.</b> Almost every structural choice here
 * mirrors a property of the production {@code RpcServer}, and the test has no meaning without it.
 * Each one is called out at its use site. The three that matter most:
 *
 * <ol>
 *   <li><b>/health is served from the SAME fixed 8-thread pool as every source call.</b> That is
 *       the whole reason the watchdog exists: when the pool drains, the engine stops answering
 *       /health even though the process is perfectly alive. Giving /health its own executor would
 *       make this fake answer while wedged, and the watchdog would never be exercised at all.</li>
 *   <li><b>/wedge parks the monitor holder OUTSIDE the pool; /busy parks it INSIDE.</b> That single
 *       difference is the entire discriminator the watchdog is built on. A holder outside the pool
 *       occupies no slot, so all N pool threads can block — a true deadlock. A holder inside the
 *       pool occupies one slot while it runs, so at most N-1 can ever block — a busy engine that is
 *       still making progress. The shipped 6-of-8 majority rule could not tell those apart and
 *       killed a healthy engine; /busy is that regression test.</li>
 *   <li><b>The RPC pool is deliberately NOT the first pool created in the process.</b> See
 *       {@code advanceGlobalPoolCounter} — that is the second HIGH defect, reproduced.</li>
 * </ol>
 *
 * <p>Configuration is by environment variable so one image covers every case:
 * <ul>
 *   <li>{@code TSUNDOKU_ENGINE_PORT} — listen port (default 7777, as in the image).</li>
 *   <li>{@code FAKE_ENGINE_THREAD_NAMES} — {@code engine-rpc} (default) names the pool threads
 *       {@code engine-rpc-<n>} exactly as the production {@code RpcThreadFactory} does;
 *       {@code jdk-default} leaves the JDK to name them {@code pool-<n>-thread-<m>}, which is the
 *       watchdog's FALLBACK path and what the image emitted before the factory was named. Both
 *       shapes must keep working, so both are tested.</li>
 *   <li>{@code FAKE_BUSY_HOLD_MS} — how long /busy's legitimate long call holds the monitor.</li>
 * </ul>
 */
public final class FakeEngine {

    /**
     * Stands in for a source extension that synchronises on itself — the object every request for
     * one source contends on. In GAP-137 the wedged monitor was an extension instance exactly like
     * this one.
     */
    private static final Object SOURCE_MONITOR = new Object();

    /** Long calls started / finished, reported by /health so the test can prove one COMPLETED. */
    private static final AtomicInteger BUSY_STARTED = new AtomicInteger();
    private static final AtomicInteger BUSY_DONE = new AtomicInteger();

    private static ExecutorService pool;

    private FakeEngine() {
    }

    public static void main(String[] args) throws IOException {
        final int port = Integer.parseInt(env("TSUNDOKU_ENGINE_PORT", "7777"));
        final boolean namedThreads = !"jdk-default".equals(env("FAKE_ENGINE_THREAD_NAMES", "engine-rpc"));

        advanceGlobalPoolCounter(namedThreads);

        final HttpServer server = HttpServer.create(new InetSocketAddress(port), 0);

        // Mirrors RpcServer.start(): ONE fixed 8-thread pool, shared by /health and every source
        // call. Size 8 is copied from production because the watchdog's rule is "every pool thread
        // we can SEE is blocked" — it is size-independent, but a fake with a different size would
        // stop reproducing how many requests it takes to drain the pool.
        pool = namedThreads
                ? Executors.newFixedThreadPool(8, rpcThreadFactory())
                : Executors.newFixedThreadPool(8);
        server.setExecutor(pool);

        // The ops endpoint the watchdog probes. `pid` and the two counters are additions this fake
        // makes for the test's benefit: they let the runner prove "the engine was NOT restarted"
        // (same pid) and "the long call completed" (busyDone went up) rather than inferring both
        // from the fact that /health started answering again — which is also what a restart looks
        // like.
        server.createContext("/health", ex -> respond(ex, "{\"status\":\"ok\",\"sources\":64"
                + ",\"pid\":" + ProcessHandle.current().pid()
                + ",\"busyStarted\":" + BUSY_STARTED.get()
                + ",\"busyDone\":" + BUSY_DONE.get() + "}"));

        // ── A TRUE DEADLOCK ──────────────────────────────────────────────────────────────────
        // The GAP-137 shape. A NON-pool thread takes the source monitor and parks forever (in
        // production: a WebView callback parked in a network wait that never returns), then every
        // pool thread piles up behind it.
        //
        // The holder occupies NO pool slot, so all 8 pool threads end up BLOCKED and the dump shows
        // 8 of 8. Nothing can ever release the monitor, so only a restart clears it — which is
        // exactly what the watchdog must do here.
        //
        // The thread name is the one GAP-137's real dump carried. The JDK's own HttpServer already
        // runs a thread called HTTP-Dispatcher, so the dump contains two; that is harmless and
        // deliberate. What the predicate cares about is only that the holder is NOT a pool thread.
        server.createContext("/wedge", ex -> {
            final Thread holder = new Thread(() -> {
                synchronized (SOURCE_MONITOR) {
                    sleepMs(Long.MAX_VALUE);
                }
            }, "HTTP-Dispatcher");
            holder.setDaemon(true);
            holder.start();
            // Let the holder actually acquire the monitor before the waiters are queued behind it.
            sleepMs(300);
            // 8 waiters for an 8-thread pool. The thread serving THIS request is one of the 8: it
            // frees its slot as soon as it has responded and then picks up the queued 8th task, so
            // the steady state really is 8 blocked threads, not 7.
            for (int i = 0; i < 8; i++) {
                pool.execute(() -> {
                    synchronized (SOURCE_MONITOR) {
                        // Reached only if the monitor is ever released. It never is.
                    }
                });
            }
            respond(ex, "wedged");
        });

        // ── SATURATION, NOT A DEADLOCK (the HIGH-1 regression case) ──────────────────────────
        // One pool thread runs a long but PROGRESSING call while holding the source monitor — a
        // per-scanlator coverage walk over a large series takes ~20 minutes against a
        // self-synchronising extension (GAP-140). Every other request for that source queues behind
        // it, /health included, so the engine looks exactly as silent as a wedged one from outside.
        //
        // The difference is only visible in the dump: the holder IS a pool thread, so it occupies a
        // slot and at most 7 of 8 can be blocked. The watchdog must read that as saturation and
        // leave the engine alone — killing it here destroys precisely the expensive work the system
        // tries hardest not to repeat, and the 600s kill cooldown is shorter than the walk, so under
        // sustained load the walk could be killed forever and never complete.
        server.createContext("/busy", ex -> {
            final long hold = Long.parseLong(env("FAKE_BUSY_HOLD_MS", "600000"));
            pool.execute(() -> {
                BUSY_STARTED.incrementAndGet();
                synchronized (SOURCE_MONITOR) {
                    sleepMs(hold);
                }
                BUSY_DONE.incrementAndGet();
            });
            sleepMs(300);
            // SEVEN, not eight: the holder above already owns the eighth slot. Raising this to 8
            // would queue a task that never gets a thread and would change nothing in the dump —
            // but lowering it would leave an idle pool thread and stop reproducing "the engine
            // cannot answer /health", which is the precondition for the watchdog to look at all.
            for (int i = 0; i < 7; i++) {
                pool.execute(() -> {
                    synchronized (SOURCE_MONITOR) {
                        // Returns as soon as the long call finishes. That is the point.
                    }
                });
            }
            respond(ex, "busy");
        });

        server.start();
        System.out.println("fake-engine: listening on " + port
                + " pid=" + ProcessHandle.current().pid()
                + " threadNames=" + (namedThreads ? "engine-rpc" : "jdk-default"));
        System.out.flush();
    }

    /**
     * Creates and disposes of an unrelated executor BEFORE the RPC pool, so the RPC pool is never
     * the first pool in the process.
     *
     * <p>Why this one-liner is load-bearing: {@code Executors.newFixedThreadPool(n)} with no factory
     * names its threads from the JDK's {@code DefaultThreadFactory}, whose pool number comes from a
     * PROCESS-GLOBAL static counter. Any library that creates a pool first shifts the RPC pool from
     * {@code pool-1-thread-*} to {@code pool-2-thread-*}. A watchdog predicate anchored to
     * {@code pool-1} then counts zero threads during a genuine permanent deadlock and never
     * recovers it — reproduced end to end. The engine-host's own pool being {@code pool-1} today is
     * a coincidence, not a contract, which is why the shipped predicate matches any pool number.
     *
     * @param keepAlive when true the decoy pool's thread is parked and therefore APPEARS in the
     *     thread dump. That is used in {@code engine-rpc} mode on purpose: it puts an unrelated
     *     {@code pool-1-thread-1} in the dump alongside the {@code engine-rpc-*} threads and so
     *     exercises the watchdog's precedence rule (named threads win outright, {@code pool-*} is
     *     ignored entirely) against a live JVM.
     *     <p>In {@code jdk-default} mode it MUST be false. The fallback path counts every
     *     {@code pool-*} thread it can see, so a lingering decoy would be counted alongside the RPC
     *     pool and the "every thread blocked" rule could not hold. Shutting the decoy down retires
     *     its thread while leaving the global counter advanced — which is exactly the condition we
     *     want to reproduce, and nothing more.
     */
    private static void advanceGlobalPoolCounter(boolean keepAlive) {
        final ExecutorService decoy = Executors.newSingleThreadExecutor();
        if (keepAlive) {
            decoy.execute(() -> sleepMs(Long.MAX_VALUE));
            return;
        }
        decoy.execute(() -> { });
        decoy.shutdown();
        try {
            decoy.awaitTermination(10, TimeUnit.SECONDS);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    /**
     * The production {@code enginehost.RpcThreadFactory}, reproduced: a PER-POOL counter and the
     * {@code engine-rpc-} prefix that {@code watchdog.sh} anchors its predicate on
     * ({@code ^"engine-rpc-[0-9]+" }). If the prefix here and the one in {@code RpcThreadFactory}
     * ever disagree, this test stops testing the shape the image actually ships.
     */
    private static ThreadFactory rpcThreadFactory() {
        final AtomicInteger next = new AtomicInteger(1);
        return r -> {
            final Thread t = new Thread(r, "engine-rpc-" + next.getAndIncrement());
            t.setDaemon(false);
            t.setPriority(Thread.NORM_PRIORITY);
            return t;
        };
    }

    private static String env(String name, String fallback) {
        final String value = System.getenv(name);
        return value == null || value.isEmpty() ? fallback : value;
    }

    private static void sleepMs(long ms) {
        try {
            Thread.sleep(ms);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private static void respond(HttpExchange ex, String body) throws IOException {
        final byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
        ex.sendResponseHeaders(200, bytes.length);
        try (OutputStream os = ex.getResponseBody()) {
            os.write(bytes);
        }
    }
}
