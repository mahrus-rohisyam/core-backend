# Development tasks. `make up` starts Postgres in Docker; `make test` needs it.
TEST_DB ?= postgres://core:core@127.0.0.1:55532/core

.PHONY: up down test cover lint vuln build run

up:
	docker compose -f compose.dev.yml up -d --wait

down:
	docker compose -f compose.dev.yml down

test:
	APP_TEST_DATABASE_URL=$(TEST_DB) go test -race -count=1 ./...

cover:
	APP_TEST_DATABASE_URL=$(TEST_DB) go test -count=1 -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "run gofmt -w ." && exit 1)
	go vet ./...
	git ls-files -z | xargs -0 python3 -I scripts/agent/check-hidden-unicode.py

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/notes ./examples/notes

# Runs the example app. Copy .env.example to .env and fill in APP_MASTER_KEY first.
run:
	set -a && . ./.env && set +a && go run ./examples/notes
