SHELL := /bin/sh
# Server-like local stack (client built into the web image). The plain docker builder sees
# locally pulled base images (buildx names it after the current context); a docker-container
# builder would go to Docker Hub for them.
LOCAL := BUILDX_BUILDER=$$(docker context show) docker compose --env-file .env -f infra/compose.dev.yaml -f infra/compose.local.yaml
.PHONY: init install infra-up infra-down dev dev-api dev-client migrate bootstrap build test test-api test-client typecheck check prod-up local local-up local-logs local-down backup restore
init:
	python3 infra/env.py
install:
	cd api && go mod download
	cd client && npm ci
infra-up:
	docker compose --env-file .env -f infra/compose.dev.yaml up -d --wait
infra-down:
	docker compose --env-file .env -f infra/compose.dev.yaml down
dev: infra-up
	mprocs
dev-api:
	cd api && python3 ../infra/run.py go run ./cmd/service
dev-client:
	cd client && npm run dev
migrate:
	cd api && python3 ../infra/run.py go run ./cmd/service migrate
bootstrap:
	python3 infra/bootstrap.py
build:
	cd api && go build -o ../bin/service ./cmd/service
	cd client && npm run build
test: test-api test-client
test-api:
	cd api && go test -race ./...
test-client:
	cd client && npm test
typecheck:
	cd client && npm run typecheck
check: typecheck test build
local:
	$(LOCAL) up --build
local-up:
	$(LOCAL) up -d --build --wait
local-logs:
	$(LOCAL) logs -f
local-down:
	$(LOCAL) down
prod-up:
	docker compose --env-file .env -f infra/compose.prod.yaml up -d --build
backup:
	python3 infra/backup.py backup
restore:
	python3 infra/backup.py restore $(ARCHIVE)
