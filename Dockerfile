# ---- build ----------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first, so a code-only change reuses this layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static binary: the runtime image has no libc to link against.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- runtime --------------------------------------------------------------
FROM alpine:3.20

RUN adduser -D -u 10001 app

WORKDIR /app

COPY --from=build /out/server /app/server
# The migrator reads these off disk at startup, so they ship with the binary.
COPY --from=build /src/migrations /app/migrations
COPY --from=build /src/seed /app/seed

USER app
EXPOSE 8080

# No .env in the image -- configuration comes from the environment.
ENV HTTP_ADDR=:8080 \
    MIGRATIONS_DIR=/app/migrations \
    SEED_DIR=/app/seed

ENTRYPOINT ["/app/server"]
