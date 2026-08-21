KUBECTL = kubectl
GO = go
IMG ?= podtetris-planner:latest
KIND_CLUSTER ?= kind
CONTAINER_TOOL ?= docker
KIND ?= kind

.PHONY: all build run clean check docker-build kind-load

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

docker-build:
	$(CONTAINER_TOOL) build -t $(IMG) .

kind-load: docker-build
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER)
