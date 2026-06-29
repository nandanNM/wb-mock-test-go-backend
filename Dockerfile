# --- Build stage ---
FROM golang:1.25-alpine AS build

WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static, stripped binary with the version stamped in.
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/server ./cmd/server

# --- Runtime stage ---
# distroless: no shell, no package manager — minimal attack surface. Includes
# CA certificates so TLS to hosted databases (Neon, etc.) works.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/server /app/server

# Runs as the non-root user provided by the distroless image.
EXPOSE 8080
ENTRYPOINT ["/app/server"]
