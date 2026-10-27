# Frontend build stage — produces internal/webui/frontend/dist/
FROM docker.io/library/node:24-alpine@sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53 AS frontend

WORKDIR /frontend

# Install dependencies first so they cache when only sources change.
COPY internal/webui/frontend/package.json internal/webui/frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund

# Build the SPA. Output goes to /frontend/dist/, copied into the Go build
# context below so //go:embed picks it up.
COPY internal/webui/frontend/ ./
RUN npm run build

# Go build stage
FROM docker.io/library/golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .

# Drop in the built frontend so embed.FS ships real assets instead of the
# .gitkeep placeholder committed to source control.
COPY --from=frontend /frontend/dist/ ./internal/webui/frontend/dist/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o glisk .

# Runtime stage
FROM docker.io/library/alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

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
