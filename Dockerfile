# Frontend build stage — produces internal/webui/frontend/dist/
FROM docker.io/library/node:24-alpine@sha256:760e44b64c78674d9c79fa32e63c0ba8f817f79791e1aa32e07669bb0bfeaeaa AS frontend

WORKDIR /frontend

# Install dependencies first so they cache when only sources change.
COPY internal/webui/frontend/package.json internal/webui/frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund

# Build the SPA. Output goes to /frontend/dist/, copied into the Go build
# context below so //go:embed picks it up.
COPY internal/webui/frontend/ ./
RUN npm run build

# Go build stage
FROM docker.io/library/golang:1.27-alpine@sha256:4cb7ac979db5fcc41cae44b2227ba5ab8a51e8807f40d9ba4dee20a0ad960b5b AS builder

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .

# Drop in the built frontend so embed.FS ships real assets instead of the
# .gitkeep placeholder committed to source control.
COPY --from=frontend /frontend/dist/ ./internal/webui/frontend/dist/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o glisk .

# Runtime stage
FROM docker.io/library/alpine:3.24@sha256:5b02b42e375f7426f8d65c3af331ca05d9878f9989230354504e0b9dfd431f60

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
