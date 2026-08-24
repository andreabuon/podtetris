KUBECTL = kubectl

.PHONY: all build-all cluster crd install deploy deploy-local uninstall clean

all: deploy

cluster:
	cd labs/lab6_kwok+kind_cost && ./setup-cluster.sh

crd:
	$(MAKE) -C src/evictor manifests

build-all:
	-$(MAKE) -C src/planner docker-build
	-$(MAKE) -C src/evictor docker-build
	-$(MAKE) -C src/webhook docker-build

kind-load:
	-$(MAKE) -C src/planner kind-load
	-$(MAKE) -C src/evictor kind-load
	-$(MAKE) -C src/webhook kind-load

deploy: crd build-all
	helm upgrade --install podtetris charts/podtetris --namespace="podtetris" --create-namespace

deploy-local: crd kind-load
	helm upgrade --install podtetris charts/podtetris --namespace="podtetris" --create-namespace

clean:
	kind delete cluster "kind"

