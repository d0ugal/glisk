# Frontend build stage — produces internal/webui/frontend/dist/
FROM docker.io/library/node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS frontend

WORKDIR /frontend

# Install dependencies first so they cache when only sources change.
COPY internal/webui/frontend/package.json internal/webui/frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund

# Build the SPA. Output goes to /frontend/dist/, copied into the Go build
# context below so //go:embed picks it up.
COPY internal/webui/frontend/ ./
RUN npm run build

# Go build stage
FROM docker.io/library/golang:1.26-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .

# Drop in the built frontend so embed.FS ships real assets instead of the
# .gitkeep placeholder committed to source control.
COPY --from=frontend /frontend/dist/ ./internal/webui/frontend/dist/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o glisk .

# Runtime stage
FROM docker.io/library/alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app
COPY --from=builder /build/glisk .

# The scan needs read access across /volume1, so the container runs as root
# (read-only mount). It writes only to /cache.
RUN mkdir -p /cache

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/glisk"]
