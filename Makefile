KUBECTL = kubectl

.PHONY: all build-all cluster cluster-boutique cluster-kwok crd install deploy deploy-local deploy-local-boutique deploy-local-kwok run-planner experiment uninstall clean

all: deploy

cluster:
	cd labs/lab6_kind && ./setup-cluster.sh

cluster-boutique:
	cd labs/lab7_boutique-kind && ./setup-cluster.sh

cluster-kwok:
	cd labs/lab8_kwok && ./setup-cluster.sh

crd:
	$(MAKE) -C src/evictor manifests

build-all:
	-$(MAKE) -C src/planner docker-build
	-$(MAKE) -C src/evictor docker-build
	-$(MAKE) -C src/webhook docker-build

KIND_CLUSTER ?= kind

kind-load:
	-$(MAKE) -C src/planner kind-load KIND_CLUSTER=$(KIND_CLUSTER)
	-$(MAKE) -C src/evictor kind-load KIND_CLUSTER=$(KIND_CLUSTER)
	-$(MAKE) -C src/webhook kind-load KIND_CLUSTER=$(KIND_CLUSTER)

deploy: crd build-all
	helm upgrade --install podtetris charts/podtetris --namespace="podtetris" --create-namespace

# Local Kind: lab cert issuer + planner rules (→ rules ConfigMap → /etc/podtetris/rules.yaml).
deploy-local: crd kind-load
	helm upgrade --install podtetris charts/podtetris --namespace="podtetris" --create-namespace -f labs/lab6_kind/values-kind.yaml -f charts/podtetris/values-kind.yaml

# Local Kind + Online Boutique planner rules (labs/lab7_boutique-kind).
deploy-local-boutique: crd kind-load
	helm upgrade --install podtetris charts/podtetris --namespace="podtetris" --create-namespace \
	  -f labs/lab7_boutique-kind/values-kind.yaml \
	  -f labs/lab7_boutique-kind/values-boutique-rules.yaml

# Local Kind + KWOK lab (labs/lab8_kwok). Cluster name is kwok, not kind.
deploy-local-kwok: crd
	$(MAKE) kind-load KIND_CLUSTER=kwok
	helm upgrade --install podtetris charts/podtetris --namespace="podtetris" --create-namespace \
	  -f labs/lab8_kwok/values-kind.yaml \
	  -f labs/lab8_kwok/values-kwok-rules.yaml

run-planner:
	$(KUBECTL) create job --from=cronjob/podtetris-planner "podtetris-planner-$$(date +%Y%m%d-%H%M%S)" -n podtetris

# Uses current kubeconfig. Cluster and chart must already be up.
experiment:
	./scripts/run-experiment.sh

clean:
	-kind delete cluster --name kind
	-kind delete cluster --name kwok

