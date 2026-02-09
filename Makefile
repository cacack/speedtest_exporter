.PHONY: all lint test test-race vuln fmt-check tidy-check build clean

all: fmt-check tidy-check lint vuln test-race build

lint:
	golangci-lint run

test:
	go test -v -count=1 ./...

test-race:
	go test -v -race -count=1 ./...

vuln:
	govulncheck ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:" && gofmt -l . && exit 1)

tidy-check:
	go mod tidy
	@git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum dirty after tidy" && exit 1)

build:
	go build -o speedtest_exporter ./cmd/speedtest_exporter/main.go

clean:
	rm -f speedtest_exporter
	rm -rf dist/
