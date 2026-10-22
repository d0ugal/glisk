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
FROM docker.io/library/golang:1.27-alpine@sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4 AS builder

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .

# Drop in the built frontend so embed.FS ships real assets instead of the
# .gitkeep placeholder committed to source control.
COPY --from=frontend /frontend/dist/ ./internal/webui/frontend/dist/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o glisk .

# Runtime stage
FROM docker.io/library/alpine:3.24@sha256:c6b073f8e9e58d881f6ffba6ebf3b9ebd60b9e7d6e8337018c0e68bb5c356873

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
