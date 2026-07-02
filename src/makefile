KUBECTL = kubectl
GO = go
MANIFESTS_DIR = ./yaml

SRCS := $(find . -name '*.go')

.PHONY: all cluster apply-configmap apply-rbac run clean 

all: run

cluster:
	cd ../labs/lab6_kwok+kind_cost && ./setup-cluster.sh

apply-configmap:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-config.yaml

apply-rbac:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

podtetris: $(SRCS)
	$(GO) build -o podtetris

run: apply-configmap apply-rbac podtetris
	./podtetris
#	./podtetris -logtostderr=true -v=3

clean:
	rm -f ./podtetris
	$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/podtetris-config.yaml -f $(MANIFESTS_DIR)/podtetris-rbac.yaml