#!/bin/sh
# entrypoint.sh — Container entry point for the Tsundoku backend.
# Passes all arguments through to the binary so Docker CMD / Compose
# `command:` overrides work transparently.
set -e
exec /app/tsundoku "$@"
