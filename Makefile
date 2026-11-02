.PHONY: all build test lint vulncheck run compose-up compose-down pair clean

GOBIN ?= $(shell go env GOPATH)/bin

all: lint vulncheck test build

build:
	go build -v -o bin/gmessages-rag ./cmd/gmessages-rag

test:
	go test -v -race -coverprofile=coverage.out ./...

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
