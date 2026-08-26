IMG ?= podtetris-webhook:latest
KIND_CLUSTER ?= kind
CONTAINER_TOOL ?= docker
KIND ?= kind

.PHONY: all docker-build kind-load

all: deploy

docker-build:
	$(CONTAINER_TOOL) build -t $(IMG) .

kind-load: docker-build
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER)