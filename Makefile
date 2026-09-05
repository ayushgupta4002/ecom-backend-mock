.PHONY: help up down run build test tidy env docker-up docker-down docker-logs

# Load .env if present so `make run` / `make test` use the same configuration
# as the server does. Real environment variables still take precedence.
ifneq (,$(wildcard .env))
include .env
export
endif

help:
	@echo "Docker (whole stack):"
	@echo "  make docker-up    - build + run Postgres and the API on :8080"
	@echo "  make docker-logs  - follow the API logs"
	@echo "  make docker-down  - stop the stack and delete its data"
	@echo ""
	@echo "Local Go development (Postgres in Docker, API on the host):"
	@echo "  make env          - create .env from .env.example"
	@echo "  make up           - start Postgres only (database 'ecom' on :5432)"
	@echo "  make run          - migrate + seed, then run the API on :8080"
	@echo "  make test         - run the full test suite"
	@echo "  make down         - stop Postgres and delete its data"

# ---------------------------------------------------------------- docker ----
docker-up:
	docker compose --profile api up -d --build
	@echo "API on http://localhost:8080  (try: curl localhost:8080/healthz)"

docker-logs:
	docker compose logs -f api

docker-down:
	docker compose --profile api down -v

# ----------------------------------------------------------------- local ----
env:
	@if [ -f .env ]; then \
		echo ".env already exists, leaving it untouched"; \
	else \
		cp .env.example .env && echo "created .env from .env.example"; \
	fi

up:
	docker compose up -d db
	@echo "waiting for postgres..."
	@until docker compose exec -T db pg_isready -U postgres -d ecom >/dev/null 2>&1; do sleep 1; done
	@echo "postgres ready"

down:
	docker compose down -v

build:
	go build ./...

run: up
	go run ./cmd/server

# The tests run against DATABASE_URL and TRUNCATE all tables between cases,
# so point it at a development database only.
test: up
	go test ./... -v -count=1

tidy:
	go mod tidy
