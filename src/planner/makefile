KUBECTL = kubectl
GO = go

.PHONY: all build run clean check

all: run

build: config.go consolidation.go main.go nodes.go permutations.go simulation.go utils.go
	GOWORK=off $(GO) build -o podtetris-planner

run: build
	./podtetris-planner --config ./config.yaml

clean:
	rm -f ./podtetris-planner

check:
	go vet .
	staticcheck ./