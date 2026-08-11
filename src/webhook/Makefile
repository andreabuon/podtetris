IMG ?= podtetris-webhook:latest
KIND_CLUSTER ?= kind
NAMESPACE ?= podtetris
SERVICE ?= podtetris-webhook
KUBECTL ?= kubectl
CONTAINER_TOOL ?= docker
KIND ?= kind
MANIFESTS_DIR = ./manifests

.PHONY: all docker-build kind-load certs deploy undeploy clean

all: deploy

docker-build:
	$(CONTAINER_TOOL) build -t $(IMG) .

kind-load: docker-build
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER)

# Generate TLS material, write the Secret manifest, and patch caBundle into
# the MutatingWebhookConfiguration.
certs:
	NAMESPACE=$(NAMESPACE) SERVICE=$(SERVICE) ./gen-certs.sh
	@cabundle=$$(openssl base64 -A < certs/tls.crt); \
	tmp=$$(mktemp); \
	sed "s|caBundle: \".*\"|caBundle: \"$$cabundle\"|" $(MANIFESTS_DIR)/mutatingwebhook.yaml > "$$tmp"; \
	mv "$$tmp" $(MANIFESTS_DIR)/mutatingwebhook.yaml

deploy: kind-load certs
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/deployment.yaml
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/webhook-certs-secret.yaml
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/deployment.yaml
	$(KUBECTL) apply -f $(MANIFESTS_DIR)/mutatingwebhook.yaml
	$(KUBECTL) -n $(NAMESPACE) rollout status deployment/podtetris-webhook --timeout=120s

undeploy:
	-$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/mutatingwebhook.yaml
	-$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/webhook-certs-secret.yaml
	-$(KUBECTL) delete --ignore-not-found=true -f $(MANIFESTS_DIR)/deployment.yaml

clean: undeploy
	rm -f certs/tls.crt certs/tls.key
