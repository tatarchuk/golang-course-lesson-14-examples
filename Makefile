# Lesson 14 examples (WebSockets). The targets grow with the parts of the homework; `make` lists them.
.DEFAULT_GOAL := help

help:          ## list the targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

run:           ## serve the examples on http://localhost:8080 (ARGS="-pong-wait 10s" etc.)
	go run ./cmd/libratrack $(ARGS)

build:         ## build the server and the terminal client into bin/
	go build -o bin/ ./cmd/libratrack ./cmd/wsclient

test:          ## every test with the race detector
	go test -race -count=1 ./...

race-demo:     ## slide 23: the race detector on the unsynchronised map (fails on purpose)
	go test -race -tags racedemo -count=1 -v ./internal/naive

cycles:        ## 5 cycles of 20 clients against the running server, goroutines before and after
	go run ./cmd/wsclient -clients 20 -cycles 5 cycles

.PHONY: help run build test race-demo cycles
