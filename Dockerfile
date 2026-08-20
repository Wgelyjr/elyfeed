# elyfeed: build the React/PWA frontend, then the Go binary that embeds it,
# then ship a minimal runtime image.

# ---- Stage 1: build the React/PWA frontend ----
FROM node:22-alpine AS web
WORKDIR /build
# Install dependencies first for layer caching.
COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm ci
COPY web/ ./web/
# Vite writes the production bundle to ../internal/web/dist (repo-relative).
RUN cd web && npm run build

# ---- Stage 2: build the static Go binary ----
FROM golang:1.25-alpine AS go
WORKDIR /src
ENV GOTOOLCHAIN=local
# Cache module downloads across source changes.
COPY go.mod go.sum ./
RUN go mod download
# Copy the Go sources. The locally-built frontend is excluded via .dockerignore;
# the authoritative bundle is pulled from the web stage below.
COPY . .
# go:embed needs the frontend present at build time.
COPY --from=web /build/internal/web/dist/ ./internal/web/dist/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/elyfeed .

# ---- Stage 3: minimal runtime ----
FROM alpine:3.20
# CA certs for HTTPS feed fetching and tzdata for timezones.
RUN apk add --no-cache ca-certificates tzdata \
    && update-ca-certificates \
    && adduser -D -H app
USER app
EXPOSE 8080
COPY --from=go /out/elyfeed /usr/local/bin/elyfeed
ENTRYPOINT ["elyfeed"]
