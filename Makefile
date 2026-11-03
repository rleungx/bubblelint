.PHONY: build test lint custom integration

build:
	go build -o bin/bubblelint ./cmd/bubblelint

test:
	go test -race ./...

custom:
	golangci-lint custom -v

lint: custom
	./bin/golangci-lint-bubble run ./...

integration: build custom
	go test ./integration -count=1 -args -golangci-lint=$(CURDIR)/bin/golangci-lint-bubble -bubblelint=$(CURDIR)/bin/bubblelint
