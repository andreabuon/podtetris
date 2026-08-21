KUBECTL = kubectl
GO = go

.PHONY: all build run clean check

all: run

podtetris-planner: *.go
	GOWORK=off $(GO) build -o podtetris-planner

build: podtetris-planner

run: podtetris-planner
	./podtetris-planner --config ./config.yaml

clean:
	rm -f ./podtetris-planner

check:
	go vet .
	staticcheck ./
