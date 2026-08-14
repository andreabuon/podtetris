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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// nodeSelectorKey is the label used to force placement.
	nodeSelectorKey = "kubernetes.io/hostname"

	conditionReasonReplacementCreated = "ReplacementCreated"
	maxClaimAttempts                  = 8
)

var errPodMoveAlreadyClaimed = errors.New("podmove already claimed for a replacement pod")

var (
	k8sClient    client.Client
	codecs       = serializer.NewCodecFactory(runtime.NewScheme())
	deserializer = codecs.UniversalDeserializer()
)

func main() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Could not load in-cluster config: %v", err)
	}

	scheme := runtime.NewScheme()
	if err := podtetrisiov1.AddToScheme(scheme); err != nil {
		log.Fatalf("Could not register PodMove scheme: %v", err)
	}
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("Could not create Kubernetes client: %v", err)
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

	var pm *podtetrisiov1.PodMove
	claimed := false
	for attempt := 0; attempt < maxClaimAttempts; attempt++ {
		var err error
		pm, err = findMatchingPodMove(ctx, &pod)
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
				pod.Namespace, podDisplayName(&pod), pm.Namespace, pm.Name)
			claimed = true
			break
		}

		if err := claimReplacement(ctx, pm, &pod); err != nil {
			if errors.Is(err, errPodMoveAlreadyClaimed) {
				log.Printf("PodMove %s/%s already claimed; looking for another open move",
					pm.Namespace, pm.Name)
				pm = nil
				continue
			}
			log.Printf("Error marking TargetNodeInjected on PodMove %s/%s: %v",
				pm.Namespace, pm.Name, err)
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
		pod.Namespace, podDisplayName(&pod), pod.GenerateName, pm.Spec.TargetNode, pm.Namespace, pm.Name)

	patch := buildMutationPatch(&pod, pm.Spec.TargetNode, pm.Name)
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

// findMatchingPodMove returns the first open PodMove that matches this pod.
// ReplicaSet and Deployment replacements match by controller owner;
// StatefulSet replacements also require spec.podRef.name to equal the incoming pod name.
func findMatchingPodMove(ctx context.Context, pod *corev1.Pod) (*podtetrisiov1.PodMove, error) {
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return nil, nil
	}

	var list podtetrisiov1.PodMoveList
	if err := k8sClient.List(ctx, &list, client.InNamespace(pod.Namespace)); err != nil {
		return nil, err
	}

	for i := range list.Items {
		pm := &list.Items[i]
		if !isOpenForReplacement(pm) || !replacementMatches(pm, pod, owner) {
			continue
		}
		if pm.Spec.TargetNode == "" {
			return nil, fmt.Errorf("PodMove %s/%s has empty spec.targetNode", pm.Namespace, pm.Name)
		}
		return pm, nil
	}
	return nil, nil
}

func replacementMatches(pm *podtetrisiov1.PodMove, pod *corev1.Pod, owner *metav1.OwnerReference) bool {
	if !ownerMatches(pm.Spec.Owner, *owner) {
		return false
	}
	if owner.Kind == "StatefulSet" {
		return pod.Name != "" && pod.Name == pm.Spec.Pod.Name
	}
	return true
}

func ownerMatches(ref, owner metav1.OwnerReference) bool {
	if ref.UID != "" && owner.UID != "" {
		return ref.UID == owner.UID
	}
	return ref.APIVersion == owner.APIVersion && ref.Kind == owner.Kind && ref.Name == owner.Name
}

// isOpenForReplacement reports whether the PodMove is armed for a replacement CREATE:
// Evicted condition is present (False=Evicting or True=Evicted) and TargetNodeInjected is not True.
func isOpenForReplacement(pm *podtetrisiov1.PodMove) bool {
	if meta.FindStatusCondition(pm.Status.Conditions, podtetrisiov1.ConditionEvicted) == nil {
		return false
	}
	return !meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionTargetNodeInjected)
}

// claimReplacement records that this PodMove's replacement CREATE has been intercepted
// by setting TargetNodeInjected=True. LastTransitionTime is the recreation timestamp.
// Returns errPodMoveAlreadyClaimed if another CREATE won the race.
func claimReplacement(ctx context.Context, pm *podtetrisiov1.PodMove, pod *corev1.Pod) error {
	if !isOpenForReplacement(pm) {
		return errPodMoveAlreadyClaimed
	}
	applyTargetNodeInjected(pm, pod)
	if err := k8sClient.Status().Update(ctx, pm); err == nil {
		return nil
	} else if !apierrors.IsConflict(err) {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &podtetrisiov1.PodMove{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pm), current); err != nil {
			return err
		}
		if !isOpenForReplacement(current) {
			return errPodMoveAlreadyClaimed
		}
		applyTargetNodeInjected(current, pod)
		return k8sClient.Status().Update(ctx, current)
	})
}

func applyTargetNodeInjected(pm *podtetrisiov1.PodMove, pod *corev1.Pod) {
	meta.SetStatusCondition(&pm.Status.Conditions, metav1.Condition{
		Type:               podtetrisiov1.ConditionTargetNodeInjected,
		Status:             metav1.ConditionTrue,
		Reason:             conditionReasonReplacementCreated,
		Message:            fmt.Sprintf("Replacement pod %s/%s recreated and pinned to node %q", pod.Namespace, podDisplayName(pod), pm.Spec.TargetNode),
		ObservedGeneration: pm.Generation,
	})
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
