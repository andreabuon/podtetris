# PODTetris fixed-node mutating webhook

Minimal mutating admission webhook that intercepts every `Pod` `CREATE` (outside
`kube-system` and its own namespace) and injects `spec.nodeSelector["kubernetes.io/hostname"] = <TARGET_NODE_NAME>`.

Every pod created while this is active gets pinned to the same fixed node, set via the `TARGET_NODE_NAME` env var (see `manifests/deployment.yaml`).

## Notes / limitations of this minimal version

- No per-pod targeting yet: every pod gets the same node name injected.
- The webhook uses `nodeSelector` intead of `spec.nodeName`.
  `nodeSelector` still goes through the real scheduler (taints, resource fit, affinity are re-checked at scheduling time);
  `nodeName` bypasses the scheduler entirely,
- `failurePolicy: Ignore` means pod creation proceeds normally if the
  webhook is unreachable.
  failurePolicy set to `Fail` means a webhook outage can block all pod scheduling.
- The self-signed cert from `gen-certs.sh` never rotates; for anything
  beyond local testing, use cert-manager to issue and auto-rotate it.

## Deploy

```bash
# 1. Build the image
docker build -t podtetris-webhook:latest ./

# 1b.a Either push the image
docker push podtetris-webhook:latest
# 1b.b or just import the image into the local kind cluster
kind load docker-image podtetris-webhook:latest

# 2. Generate TLS cert/key as a Secret
# 2.1 Create the podtetris namespace first
kubectl apply -f manifests/deployment.yaml
# 2.2 Generate TLS cert/key as a Secret
NAMESPACE=podtetris SERVICE=podtetris-webhook ./gen-certs.sh
kubectl apply -f manifests/webhook-certs-secret.yaml

# 3. Paste the printed caBundle into manifests/mutatingwebhook.yaml, then apply the rest
kubectl apply -f manifests/deployment.yaml
kubectl apply -f manifests/mutatingwebhook.yaml
```
