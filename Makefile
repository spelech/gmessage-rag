.PHONY: all build test lint run compose-up compose-down pair clean

all: lint test build

build:
	go build -v -o bin/gmessages-rag ./cmd/gmessages-rag

test:
	go test -v -race -coverprofile=coverage.out ./...

lint:
	test -z "$$(gofmt -l .)" || (echo "Unformatted files found:" && gofmt -d . && exit 1)
	go vet ./...

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
