KUBECTL = kubectl

.PHONY: all build-all cluster crd install deploy deploy-local run-planner experiment experiment-aws uninstall clean

all: deploy

cluster:
	cd labs/lab6_kind && ./setup-cluster.sh

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

# Local Kind: lab cert issuer + planner cost rules (→ costs ConfigMap → /etc/podtetris/costs.yaml).
# See charts/podtetris/values-kind.yaml for rule → workload cost mapping.
deploy-local: crd kind-load
	helm upgrade --install podtetris charts/podtetris --namespace="podtetris" --create-namespace -f labs/lab6_kind/values-kind.yaml -f charts/podtetris/values-kind.yaml

run-planner:
	$(KUBECTL) create job --from=cronjob/podtetris-planner "podtetris-planner-$$(date +%Y%m%d-%H%M%S)" -n podtetris

# Uses current kubeconfig. Create the cluster separately with: make cluster
experiment:
	./scripts/run-experiment.sh --local

experiment-aws:
	./scripts/run-experiment.sh --aws

clean:
	kind delete cluster --name kind

