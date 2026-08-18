KUBECTL = kubectl
GO = go
MANIFESTS_DIR = ./manifests
EVICTOR_DIR = ../evictor

.PHONY: all apply-configmap apply-rbac crd install build run clean check

all: run

apply-configmap:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-config.yaml

apply-rbac:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

crd:
	$(MAKE) -C $(EVICTOR_DIR) install

install: crd apply-configmap apply-rbac

build: config.go consolidation.go main.go nodes.go permutations.go simulation.go utils.go
	GOWORK=off $(GO) build -o podtetris

run: install build
	./podtetris

clean:
	rm -f ./podtetris
	$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/podtetris-config.yaml -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

check:
	go vet .
	staticcheck ./