// PODTetris mutating admission webhook
// Intercepts every Pod CREATE request and injects a nodeSelector pinning the pod to a single, fixed node name.
// The target node is a hardcoded constant (see targetNodeName below) or an override read once at startup from the TARGET_NODE_NAME env var.
package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

// targetNodeName is the fixed node every intercepted pod will be pinned to.
// Can be overridden at startup via the TARGET_NODE_NAME env var.
const defaultTargetNodeName = "kind-worker"

// nodeSelectorKey is the label used to force placement.
const nodeSelectorKey = "kubernetes.io/hostname"

var (
	targetNodeName string

	codecs       = serializer.NewCodecFactory(runtime.NewScheme())
	deserializer = codecs.UniversalDeserializer()
)

func main() {
	targetNodeName = os.Getenv("TARGET_NODE_NAME")
	if targetNodeName == "" {
		targetNodeName = defaultTargetNodeName
	}

	certFile := getEnvOrDefault("TLS_CERT_FILE", "/etc/webhook/certs/tls.crt")
	keyFile := getEnvOrDefault("TLS_KEY_FILE", "/etc/webhook/certs/tls.key")
	addr := getEnvOrDefault("LISTEN_ADDR", ":8443")

	log.Printf("PODTetris webhook starting. Fixed target node: %q", targetNodeName)

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate", handleMutate)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig:    &tls.Config{MinVersion: tls.VersionTLS12},
	}

	log.Printf("Listening on %s", addr)
	if err := server.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Webhook server failed: %v", err)
	}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// handleMutate is the HTTP entrypoint the apiserver calls for every
// AdmissionReview matching the MutatingWebhookConfiguration rules.
func handleMutate(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not read request body: %v", err), http.StatusBadRequest)
		return
	}

	review := admissionv1.AdmissionReview{}
	if _, _, err := deserializer.Decode(body, nil, &review); err != nil {
		http.Error(w, fmt.Sprintf("could not decode AdmissionReview: %v", err), http.StatusBadRequest)
		return
	}

	if review.Request == nil {
		http.Error(w, "AdmissionReview.Request is nil", http.StatusBadRequest)
		return
	}

	response := buildAdmissionResponse(review.Request)

	responseReview := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: response,
	}

	respBytes, err := json.Marshal(responseReview)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errors.New("empty request body")
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

// buildAdmissionResponse decides what patch (if any) to return for the incoming pod.
// All pods admitted while this webhook is enabled are pinned to targetNodeName via nodeSelector.
func buildAdmissionResponse(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	pod := corev1.Pod{}
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		log.Printf("Error unmarshalling pod: %v", err)
		return &admissionv1.AdmissionResponse{
			UID:     req.UID,
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("could not unmarshal pod: %v", err),
			},
		}
	}

	log.Printf("Intercepted CREATE for pod %s/%s (generateName=%q) -> pinning to node %q",
		pod.Namespace, podDisplayName(&pod), pod.GenerateName, targetNodeName)

	patch := buildNodeSelectorPatch(pod.Spec.NodeSelector)
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		log.Printf("Error marshalling patch: %v", err)
		return &admissionv1.AdmissionResponse{
			UID:     req.UID,
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("could not marshal patch: %v", err),
			},
		}
	}

	patchType := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		UID:       req.UID,
		Allowed:   true,
		Patch:     patchBytes,
		PatchType: &patchType,
	}
}

func podDisplayName(pod *corev1.Pod) string {
	if pod.Name != "" {
		return pod.Name
	}
	return pod.GenerateName + "<pending-name>"
}

// buildNodeSelectorPatch returns a JSONPatch that sets the target node label.
// It adds the /spec/nodeSelector object if the pod has none yet, otherwise it merges the single key into the existing map.
func buildNodeSelectorPatch(existing map[string]string) []map[string]interface{} {
	if len(existing) == 0 {
		return []map[string]interface{}{
			{
				"op":   "add",
				"path": "/spec/nodeSelector",
				"value": map[string]string{
					nodeSelectorKey: targetNodeName,
				},
			},
		}
	}

	// nodeSelector already present: add/overwrite just our key so any other selector requirements set by the pod template are preserved.
	return []map[string]interface{}{
		{
			"op":    "add",
			"path":  "/spec/nodeSelector/" + jsonPatchEscape(nodeSelectorKey),
			"value": targetNodeName,
		},
	}
}

// jsonPatchEscape escapes '~' and '/' per RFC 6901, needed because map keys
// used as JSON Patch path segments must not contain raw '/' or '~'.
func jsonPatchEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
