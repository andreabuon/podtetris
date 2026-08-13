// PODTetris mutating admission webhook
// Intercepts Pod CREATE requests, finds a matching open PodMove (Evicted condition
// present, TargetNodeInjected not True), pins the pod to PodMove.spec.targetNode
// via nodeSelector, and marks TargetNodeInjected=True so the recreation is recorded
// on the PodMove (LastTransitionTime is the recreation timestamp).
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

const (
	// nodeSelectorKey is the label used to force placement.
	nodeSelectorKey = "kubernetes.io/hostname"

	conditionReasonReplacementCreated = "ReplacementCreated"
	maxClaimAttempts                  = 8
)

var errPodMoveAlreadyClaimed = errors.New("podmove already claimed for a replacement pod")

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
// Pods with a matching open PodMove are pinned to its targetNode, and the PodMove
// is marked TargetNodeInjected=True so a later CREATE cannot claim the same move.
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

	dryRun := req.DryRun != nil && *req.DryRun

	var pm *unstructured.Unstructured
	var targetNode string
	claimed := false
	for attempt := 0; attempt < maxClaimAttempts; attempt++ {
		var err error
		pm, targetNode, err = findMatchingPodMove(ctx, &pod)
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
			break
		}

		if dryRun {
			log.Printf("Dry-run CREATE for pod %s/%s; skipping TargetNodeInjected update on PodMove %s/%s",
				pod.Namespace, podDisplayName(&pod), pm.GetNamespace(), pm.GetName())
			claimed = true
			break
		}

		if err := claimReplacement(ctx, pm, &pod, targetNode); err != nil {
			if errors.Is(err, errPodMoveAlreadyClaimed) {
				log.Printf("PodMove %s/%s already claimed; looking for another open move",
					pm.GetNamespace(), pm.GetName())
				pm = nil
				continue
			}
			log.Printf("Error marking TargetNodeInjected on PodMove %s/%s: %v",
				pm.GetNamespace(), pm.GetName(), err)
			return &admissionv1.AdmissionResponse{
				UID:     req.UID,
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("could not mark PodMove replacement: %v", err),
				},
			}
		}
		claimed = true
		break
	}
	if !claimed {
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

	list, err := dynClient.Resource(podMoveGVR).Namespace(pod.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, "", err
	}

	var matched *unstructured.Unstructured
	for i := range list.Items {
		item := &list.Items[i]
		if !isOpenForReplacement(item) {
			continue
		}
		if !ownerMatches(item, owner) {
			continue
		}
		if matched != nil {
			log.Printf("Multiple open PodMoves for owner %s/%s in %s; using %s",
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

// isOpenForReplacement reports whether the PodMove is armed for a replacement CREATE:
// Evicted condition is present (False=Evicting or True=Evicted) and TargetNodeInjected is not True.
func isOpenForReplacement(pm *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(pm.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}

	evictedPresent := false
	targetInjected := false
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(cond, "type")
		condStatus, _, _ := unstructured.NestedString(cond, "status")
		switch condType {
		case podtetrisiov1.ConditionEvicted:
			evictedPresent = true
		case podtetrisiov1.ConditionTargetNodeInjected:
			if condStatus == string(metav1.ConditionTrue) {
				targetInjected = true
			}
		}
	}
	return evictedPresent && !targetInjected
}

// claimReplacement records that this PodMove's replacement CREATE has been intercepted
// by setting TargetNodeInjected=True. LastTransitionTime is the recreation timestamp.
// Returns errPodMoveAlreadyClaimed if another CREATE won the race.
func claimReplacement(ctx context.Context, pm *unstructured.Unstructured, pod *corev1.Pod, targetNode string) error {
	if !isOpenForReplacement(pm) {
		return errPodMoveAlreadyClaimed
	}
	applyTargetNodeInjected(pm, pod, targetNode)
	_, err := dynClient.Resource(podMoveGVR).Namespace(pm.GetNamespace()).UpdateStatus(ctx, pm, metav1.UpdateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsConflict(err) {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, getErr := dynClient.Resource(podMoveGVR).Namespace(pm.GetNamespace()).Get(ctx, pm.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		if !isOpenForReplacement(current) {
			return errPodMoveAlreadyClaimed
		}
		applyTargetNodeInjected(current, pod, targetNode)
		_, updateErr := dynClient.Resource(podMoveGVR).Namespace(pm.GetNamespace()).UpdateStatus(ctx, current, metav1.UpdateOptions{})
		return updateErr
	})
}

func applyTargetNodeInjected(pm *unstructured.Unstructured, pod *corev1.Pod, targetNode string) {
	if _, found, _ := unstructured.NestedMap(pm.Object, "status"); !found {
		_ = unstructured.SetNestedMap(pm.Object, map[string]interface{}{}, "status")
	}

	conditions, _, _ := unstructured.NestedSlice(pm.Object, "status", "conditions")
	now := metav1.Now().UTC().Format(time.RFC3339)
	msg := fmt.Sprintf("Replacement pod %s/%s recreated and pinned to node %q", pod.Namespace, podDisplayName(pod), targetNode)

	replaced := false
	for i, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(cond, "type")
		if condType != podtetrisiov1.ConditionTargetNodeInjected {
			continue
		}
		prevStatus, _, _ := unstructured.NestedString(cond, "status")
		cond["status"] = string(metav1.ConditionTrue)
		cond["reason"] = conditionReasonReplacementCreated
		cond["message"] = msg
		if prevStatus != string(metav1.ConditionTrue) || cond["lastTransitionTime"] == nil {
			cond["lastTransitionTime"] = now
		}
		cond["observedGeneration"] = pm.GetGeneration()
		conditions[i] = cond
		replaced = true
		break
	}
	if !replaced {
		conditions = append(conditions, map[string]interface{}{
			"type":               podtetrisiov1.ConditionTargetNodeInjected,
			"status":             string(metav1.ConditionTrue),
			"reason":             conditionReasonReplacementCreated,
			"message":            msg,
			"lastTransitionTime": now,
			"observedGeneration": pm.GetGeneration(),
		})
	}
	_ = unstructured.SetNestedSlice(pm.Object, conditions, "status", "conditions")
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
