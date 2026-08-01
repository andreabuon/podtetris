#!/usr/bin/env bash
# Generates a self-signed TLS cert/key for the webhook and a Secret manifest
# containing them, plus prints the CA bundle you need to paste into
# manifests/mutatingwebhook.yaml (caBundle field).
#
# For production, use cert-manager instead of this script — this is fine for
# testing but the cert never rotates.
set -euo pipefail

NAMESPACE="${NAMESPACE:-podtetris}"
SERVICE="${SERVICE:-podtetris-webhook}"
OUT_DIR="$(dirname "$0")/certs"
mkdir -p "$OUT_DIR"

CN="${SERVICE}.${NAMESPACE}.svc"

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${OUT_DIR}/tls.key" \
  -out "${OUT_DIR}/tls.crt" \
  -days 3650 \
  -subj "/CN=${CN}" \
  -addext "subjectAltName=DNS:${CN},DNS:${SERVICE}.${NAMESPACE}.svc.cluster.local"

kubectl create secret tls podtetris-webhook-certs \
  --namespace "${NAMESPACE}" \
  --cert="${OUT_DIR}/tls.crt" \
  --key="${OUT_DIR}/tls.key" \
  --dry-run=client -o yaml > "$(dirname "$0")/manifests/webhook-certs-secret.yaml"

echo
echo "Cert generated. caBundle for manifests/mutatingwebhook.yaml:"
openssl base64 -A < "${OUT_DIR}/tls.crt"
echo
