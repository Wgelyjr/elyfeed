SHELL := /bin/bash
PORT ?= 8080
DB_URL ?= postgres://elyfeed:elyfeed@localhost:5432/elyfeed?sslmode=disable

.PHONY: all build run dev build-web test vet up down logs build-image db-up db-down db-logs icons clean

all: build

# Build the web app into the Go embed dir, then the Go binary.
build: build-web
	go build -o bin/elyfeed .

# Run the server (assumes Postgres is up).
run:
	DATABASE_URL=$(DB_URL) PORT=$(PORT) go run .

# Frontend dev server with HMR + API proxy to :8080.
dev:
	cd web && npm run dev

# Install + build the frontend into internal/web/dist.
build-web:
	cd web && npm install && npm run build

test:
	go test ./...

vet:
	go vet ./...

# Build the container image.
build-image:
	podman build -t elyfeed:latest -f Dockerfile .

# Build (if needed) and start the full stack (db + app).
# --force-recreate app: podman-compose does not detect image changes on its
# own, so recreate the app to guarantee the freshly built image is running.
# Host port for the app defaults to 8180; override with ELYFEED_PORT=....
up:
	podman compose up -d --build --force-recreate app

# Stop and remove the stack (data volume is preserved).
down:
	podman compose down

logs:
	podman compose logs -f

# Start only Postgres (for native `make run` dev workflow).
db-up:
	podman compose up -d db

db-down:
	podman compose down

db-logs:
	podman compose logs -f db

# Regenerate PWA icons (requires ImageMagick + a Noto Sans Bold font).
icons:
	@bash scripts/make-icons.sh

clean:
	rm -rf bin internal/web/dist
