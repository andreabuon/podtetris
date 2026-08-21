MANIFESTS_DIR = ./manifests
KUBECTL = kubectl
EVICTOR_DIR = evictor

.PHONY: all cluster crd install deploy uninstall clean

all: deploy

cluster:
	cd ../labs/lab6_kwok+kind_cost && ./setup-cluster.sh

crd:
	$(MAKE) -C $(EVICTOR_DIR) install

install: crd

deploy: crd
	-$(MAKE) -C evictor deploy
	-$(MAKE) -C webhook deploy

uninstall:
	-$(MAKE) -C webhook undeploy
	-$(MAKE) -C evictor undeploy
#	//TODO remove CRD too?

clean:
	kind delete cluster "kind"