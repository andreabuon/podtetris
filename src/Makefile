MANIFESTS_DIR = ./manifests
KUBECTL = kubectl
EVICTOR_DIR = evictor

.PHONY: all cluster apply-configmap apply-rbac crd install deploy uninstall clean

all: deploy

cluster:
	cd ../labs/lab6_kwok+kind_cost && ./setup-cluster.sh

apply-configmap:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-config.yaml

apply-rbac:
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/podtetris-rbac.yaml

crd:
	$(MAKE) -C $(EVICTOR_DIR) install

install: apply-configmap apply-rbac crd

deploy: apply-configmap apply-rbac crd
	-$(MAKE) -C evictor deploy
	-$(MAKE) -C webhook deploy

uninstall:
	$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/podtetris-config.yaml -f $(MANIFESTS_DIR)/podtetris-rbac.yaml
	-$(MAKE) -C webhook undeploy
	-$(MAKE) -C evictor undeploy
#	//TODO remove CRD too?

clean:
	kind delete cluster "kind"