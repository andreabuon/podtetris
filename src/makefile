KUBECTL = kubectl
GO = go
MANIFESTS_DIR = ./yaml

.PHONY: all build apply-configmap apply-rbac run clean

all: run

build:
	$(GO) build

apply-configmap:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-config.yaml

apply-rbac:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

run: build apply-configmap apply-rbac
	./podtetris

clean:
	rm -f ./podtetris
	$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/podtetris-config.yaml -f $(MANIFESTS_DIR)/podtetris-rbac.yaml