.PHONY: all build test test-integration test-live lint vulncheck run compose-up compose-down pair clean

GOBIN ?= $(shell go env GOPATH)/bin

all: lint vulncheck test build

build:
	go build -v -o bin/gmessages-rag ./cmd/gmessages-rag

# Hermetic test suite (HARN-03): the `gm` package installs a deny-all network transport,
# so any accidental outbound request fails the run. No network access required.
test:
	go test -v -race -coverprofile=coverage.out ./...

# DB-backed integration suite (DB-07): requires a reachable Postgres+pgvector DATABASE_URL.
# The internal/db tests skip themselves when DATABASE_URL is unset.
test-integration:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL must be set for test-integration" && exit 1)
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

# Live integration tests (HARN-03/HARN-04): quarantined behind the `live` build tag.
# Requires real network access and explicit opt-in; never run in CI.
test-live:
	GM_ALLOW_NETWORK=1 LIVE_GM_TEST=1 go test -tags live -v -race ./...

# Compile-check the live-tagged tests without executing them (CI-safe, no network).
test-live-compile:
	go vet -tags live ./...

lint:
	test -z "$$(gofmt -l .)" || (echo "Unformatted files found:" && gofmt -d . && exit 1)
	go vet ./...

vulncheck:
	@which govulncheck > /dev/null 2>&1 || (echo "Installing govulncheck..." && go install golang.org/x/vuln/cmd/govulncheck@latest)
	@PATH="$(GOBIN):$$PATH" govulncheck ./...

run: build
	./bin/gmessages-rag serve

compose-up:
	docker compose up -d --build

compose-down:
	docker compose down

pair:
	go run ./cmd/gmessages-rag pair

clean:
	rm -rf bin/ out/ coverage.out
