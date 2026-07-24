KUBECTL = kubectl
GO = go
MANIFESTS_DIR = ./yaml

.PHONY: all cluster apply-configmap apply-rbac run clean check

all: run

cluster:
	cd ../labs/lab6_kwok+kind_cost && ./setup-cluster.sh

apply-configmap:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-config.yaml

apply-rbac:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

podtetris: config.go consolidation.go main.go nodes.go permutations.go simulation.go utils.go
	$(GO) build -o podtetris

run: apply-configmap apply-rbac podtetris
	./podtetris
#	./podtetris -logtostderr=true -v=3

clean:
	rm -f ./podtetris
	$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/podtetris-config.yaml -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

check:
	go vet .
	staticcheck ./