KUBECTL = kubectl
GO = go
MANIFESTS_DIR = ./manifests

.PHONY: all cluster apply-configmap apply-rbac run clean check

all: run

cluster:
	cd ../../labs/lab6_kwok+kind_cost && ./setup-cluster.sh

apply-configmap:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-config.yaml

apply-rbac:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

build: config.go consolidation.go main.go nodes.go permutations.go simulation.go utils.go
	GOWORK=off $(GO) build -o podtetris

run: apply-configmap apply-rbac build
	./podtetris
#	./podtetris -logtostderr=true -v=3

clean:
	rm -f ./podtetris
	$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/podtetris-config.yaml -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

check:
	go vet .
	staticcheck ./