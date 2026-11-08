# Frontend build stage — produces internal/webui/frontend/dist/
FROM docker.io/library/node:26-alpine@sha256:8996bb5821d62a8fd9bb27c1cb07462f3fdce6e47d640c6a881ead0b064ce216 AS frontend

WORKDIR /frontend

# Install dependencies first so they cache when only sources change.
COPY internal/webui/frontend/package.json internal/webui/frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund

# Build the SPA. Output goes to /frontend/dist/, copied into the Go build
# context below so //go:embed picks it up.
COPY internal/webui/frontend/ ./
RUN npm run build

# Go build stage
FROM docker.io/library/golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS builder

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .

# Drop in the built frontend so embed.FS ships real assets instead of the
# .gitkeep placeholder committed to source control.
COPY --from=frontend /frontend/dist/ ./internal/webui/frontend/dist/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o glisk .

# Runtime stage
FROM docker.io/library/alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

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
