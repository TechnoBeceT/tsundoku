# Dockerfile — the wedge-watchdog end-to-end harness image (GAP-137).
#
# This is NOT the product image. It is the smallest container in which the REAL entrypoint.sh and
# the REAL watchdog.sh can supervise a REAL JVM, so the watchdog's decisions are driven by an actual
# SIGQUIT thread dump rather than a checked-in text fixture.
#
# The build context is assembled by run.sh in a temporary directory: this file, FakeEngine.java, and
# COPIES of the repository's entrypoint.sh and watchdog.sh. Those two are copied rather than being
# reached with a repo-root build context because the repo-root context is large and because run.sh's
# fault-injection modes need to mutate the copy without ever touching the tracked file.
#
# Everything the product image provides and the watchdog depends on is stood in for below. Nothing
# else is: the entrypoint runs verbatim, including its supervision loops.
FROM eclipse-temurin:21-jdk-noble

# procps: pkill, used by the entrypoint's display and CEF-orphan reaping.
# curl:   the /health probe, in both the entrypoint's boot wait and the watchdog loop.
# gosu:   the PUID/PGID user switch (unused at PUID=0, but the entrypoint resolves it).
# netcat-openbsd: only to create the X display socket in the Xvfb stand-in below.
RUN apt-get update \
    && apt-get install -y --no-install-recommends procps curl gosu netcat-openbsd \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY FakeEngine.java /src/FakeEngine.java
RUN mkdir -p /app/engine-host/lib && javac -d /app/engine-host/lib /src/FakeEngine.java

# Stand-in for the Gradle start script at the path entrypoint.sh launches. It EXECs the JVM exactly
# as the real launcher does, which is what makes `$!` in supervise_engine the JVM's own pid — send
# SIGQUIT to anything else and no thread dump is ever produced.
RUN mkdir -p /app/engine-host/bin \
    && printf '#!/bin/sh\nexec java -cp /app/engine-host/lib FakeEngine\n' \
       > /app/engine-host/bin/tsundoku-engine-host \
    && chmod +x /app/engine-host/bin/tsundoku-engine-host

# Stand-in for Xvfb. The entrypoint waits (bounded, <=15s) for the X display SOCKET before it
# considers the display up, so creating a real unix socket here removes 15 seconds of dead time from
# every case. If nc is ever unavailable the socket simply never appears and the entrypoint proceeds
# after its bounded wait with a warning — slower, never broken.
#
# The final `sleep` is what the entrypoint's supervise_display waits on. It must NOT exit: a display
# that dies makes supervise_display bounce the engine-host, which would restart the very process
# these tests are asserting about.
RUN printf '%s\n' \
    '#!/bin/sh' \
    'mkdir -p /tmp/.X11-unix' \
    'rm -f /tmp/.X11-unix/X99' \
    'nc -lkU /tmp/.X11-unix/X99 >/dev/null 2>&1 &' \
    'exec sleep 2147483647' \
    > /usr/local/bin/Xvfb && chmod +x /usr/local/bin/Xvfb

# Stand-in for the Go server. entrypoint.sh execs this as its foreground process, so the container
# lives exactly as long as it does.
RUN printf '%s\n' \
    '#!/bin/sh' \
    'echo "tsundoku: foreground stand-in up"' \
    'while true; do sleep 5; done' \
    > /app/tsundoku && chmod +x /app/tsundoku

# The two files under test, verbatim from the repository (modulo run.sh's opt-in fault injection).
COPY entrypoint.sh /app/entrypoint.sh
COPY watchdog.sh /app/watchdog.sh
RUN chmod +x /app/entrypoint.sh

ENV TSUNDOKU_ENGINE_PORT=7777 TSUNDOKU_ENGINE_DATA=/config/engine
ENTRYPOINT ["/app/entrypoint.sh"]
