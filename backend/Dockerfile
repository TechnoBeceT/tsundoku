# Multi-stage Dockerfile for the Tsundoku backend.
#
# Stage 1 (builder): compiles the Go binary with CGO disabled so the binary
# is fully static and can run in a scratch/distroless image.
# Stage 2 (runtime): minimal image that ships only the binary + entrypoint.

# ---- builder ----------------------------------------------------------------
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy dependency manifests first so Docker caches the module layer separately
# from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /tsundoku ./cmd/tsundoku

# ---- runtime ----------------------------------------------------------------
FROM alpine:3.21 AS runtime

RUN addgroup -S tsundoku && adduser -S tsundoku -G tsundoku

WORKDIR /app

COPY --from=builder /tsundoku /app/tsundoku
COPY entrypoint.sh /app/entrypoint.sh

RUN chmod +x /app/entrypoint.sh

USER tsundoku

EXPOSE 9833

ENTRYPOINT ["/app/entrypoint.sh"]
