// PODTetris mutating admission webhook
// Intercepts Pod CREATE requests, finds a matching PodMove in AwaitingReplacement,
// and pins the pod to PodMove.spec.targetNode via nodeSelector.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	// nodeSelectorKey is the label used to force placement.
	nodeSelectorKey = "kubernetes.io/hostname"
)

var (
	podMoveGVR = schema.GroupVersionResource{
		Group:    "podtetris.io.podtetris.io",
		Version:  "v1",
		Resource: "podmoves",
	}

	dynClient    dynamic.Interface
	codecs       = serializer.NewCodecFactory(runtime.NewScheme())
	deserializer = codecs.UniversalDeserializer()
)

func main() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Could not load in-cluster config: %v", err)
	}
	dynClient, err = dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Could not create dynamic client: %v", err)
	}

	certFile := getEnvOrDefault("TLS_CERT_FILE", "/etc/webhook/certs/tls.crt")
	keyFile := getEnvOrDefault("TLS_KEY_FILE", "/etc/webhook/certs/tls.key")
	addr := getEnvOrDefault("LISTEN_ADDR", ":8443")

	log.Printf("PODTetris webhook starting...")

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

	response := buildAdmissionResponse(r.Context(), review.Request)

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
// Pods with a matching AwaitingReplacement PodMove are pinned to its targetNode.
func buildAdmissionResponse(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
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

	if pod.Namespace == "" {
		pod.Namespace = req.Namespace
	}

	pm, targetNode, err := findMatchingPodMove(ctx, &pod)
	if err != nil {
		log.Printf("Error looking up PodMove for pod %s/%s: %v", pod.Namespace, podDisplayName(&pod), err)
		return &admissionv1.AdmissionResponse{
			UID:     req.UID,
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("could not look up PodMove: %v", err),
			},
		}
	}
	if pm == nil {
		log.Printf("No matching PodMove for pod %s/%s; allowing without mutation",
			pod.Namespace, podDisplayName(&pod))
		return &admissionv1.AdmissionResponse{
			UID:     req.UID,
			Allowed: true,
		}
	}

	log.Printf("Intercepted CREATE for pod %s/%s (generateName=%q) -> pinning to node %q from PodMove %s/%s",
		pod.Namespace, podDisplayName(&pod), pod.GenerateName, targetNode, pm.GetNamespace(), pm.GetName())

	patch := buildMutationPatch(&pod, targetNode, pm.GetName())
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

// findMatchingPodMove returns the open PodMove for this pod's controller owner, if any.
func findMatchingPodMove(ctx context.Context, pod *corev1.Pod) (*unstructured.Unstructured, string, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return nil, "", nil
	}

	list, err := dynClient.Resource(podMoveGVR).Namespace(pod.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.Set{podtetrisiov1.PhaseLabelKey: podtetrisiov1.PhaseAwaitingReplacement}.String(),
	})
	if err != nil {
		return nil, "", err
	}

	var matched *unstructured.Unstructured
	for i := range list.Items {
		item := &list.Items[i]
		if !ownerMatches(item, owner) {
			continue
		}
		if matched != nil {
			log.Printf("Multiple AwaitingReplacement PodMoves for owner %s/%s in %s; using %s",
				owner.Kind, owner.Name, pod.Namespace, matched.GetName())
			break
		}
		matched = item
	}
	if matched == nil {
		return nil, "", nil
	}

	targetNode, found, err := unstructured.NestedString(matched.Object, "spec", "targetNode")
	if err != nil {
		return nil, "", err
	}
	if !found || targetNode == "" {
		return nil, "", fmt.Errorf("PodMove %s/%s has empty spec.targetNode", matched.GetNamespace(), matched.GetName())
	}
	return matched, targetNode, nil
}

func ownerMatches(pm *unstructured.Unstructured, owner *metav1.OwnerReference) bool {
	apiVersion, _, _ := unstructured.NestedString(pm.Object, "spec", "ownerRef", "apiVersion")
	kind, _, _ := unstructured.NestedString(pm.Object, "spec", "ownerRef", "kind")
	name, _, _ := unstructured.NestedString(pm.Object, "spec", "ownerRef", "name")
	uid, _, _ := unstructured.NestedString(pm.Object, "spec", "ownerRef", "uid")

	if uid != "" && string(owner.UID) != "" && uid == string(owner.UID) {
		return true
	}
	return apiVersion == owner.APIVersion && kind == owner.Kind && name == owner.Name
}

// buildMutationPatch pins the pod to targetNode and labels it with the PodMove name.
func buildMutationPatch(pod *corev1.Pod, targetNode, podMoveName string) []map[string]interface{} {
	patch := buildNodeSelectorPatch(pod.Spec.NodeSelector, targetNode)
	patch = append(patch, buildPodMoveLabelPatch(pod.Labels, podMoveName)...)
	return patch
}

// buildNodeSelectorPatch returns a JSONPatch that sets the target node label.
// It adds the /spec/nodeSelector object if the pod has none yet, otherwise it merges the single key into the existing map.
func buildNodeSelectorPatch(existing map[string]string, targetNodeName string) []map[string]interface{} {
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

func buildPodMoveLabelPatch(existing map[string]string, podMoveName string) []map[string]interface{} {
	if len(existing) == 0 {
		return []map[string]interface{}{
			{
				"op":   "add",
				"path": "/metadata/labels",
				"value": map[string]string{
					podtetrisiov1.PodMoveLabelKey: podMoveName,
				},
			},
		}
	}
	return []map[string]interface{}{
		{
			"op":    "add",
			"path":  "/metadata/labels/" + jsonPatchEscape(podtetrisiov1.PodMoveLabelKey),
			"value": podMoveName,
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
