KUBECTL = kubectl
GO = go
IMG ?= podtetris-planner:latest
KIND_CLUSTER ?= kind
CONTAINER_TOOL ?= docker
KIND ?= kind

.PHONY: all build run clean check docker-build kind-load

all: run

podtetris-planner: *.go
	$(GO) build -o podtetris-planner

build: podtetris-planner

run: podtetris-planner
	./podtetris-planner --config ./config.yaml

clean:
	rm -f ./podtetris-planner

check:
	go vet .
	staticcheck ./

# Context is src/ so the image can replace-build against local evictor.
docker-build:
	$(CONTAINER_TOOL) build -t $(IMG) -f Dockerfile ..

kind-load: docker-build
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER)
