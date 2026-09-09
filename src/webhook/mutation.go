package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

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

	resp := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: buildAdmissionResponse(r.Context(), review.Request),
	}

	out, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(out); err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

// buildAdmissionResponse decides what patch (if any) to return for the incoming pod.
// Matching open PodMoves are claimed and the pod is pinned to their targetNode.
func buildAdmissionResponse(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	pod, err := decodePod(req)
	if err != nil {
		log.Printf("Error unmarshalling pod: %v", err)
		return deny(req.UID, fmt.Sprintf("could not unmarshal pod: %v", err))
	}

	dryRun := req.DryRun != nil && *req.DryRun
	pm, err := claimOpenPodMove(ctx, pod, dryRun)
	if err != nil {
		log.Printf("Error claiming PodMove for pod %s/%s: %v", pod.Namespace, podDisplayName(pod), err)
		return deny(req.UID, err.Error())
	}
	if pm == nil {
		log.Printf("No matching PodMove for pod %s/%s; allowing without mutation",
			pod.Namespace, podDisplayName(pod))
		return allow(req.UID)
	}

	log.Printf("Intercepted CREATE for pod %s/%s (generateName=%q) -> pinning to node %q from PodMove %s/%s",
		pod.Namespace, podDisplayName(pod), pod.GenerateName, pm.Spec.TargetNode, pm.Namespace, pm.Name)

	patchBytes, err := json.Marshal(buildMutationPatch(pod, pm.Spec.TargetNode, pm.Name))
	if err != nil {
		log.Printf("Error marshalling patch: %v", err)
		return deny(req.UID, fmt.Sprintf("could not marshal patch: %v", err))
	}
	return allowPatched(req.UID, patchBytes)
}

func decodePod(req *admissionv1.AdmissionRequest) (*corev1.Pod, error) {
	pod := &corev1.Pod{}
	if err := json.Unmarshal(req.Object.Raw, pod); err != nil {
		return nil, err
	}
	if pod.Namespace == "" {
		pod.Namespace = req.Namespace
	}
	return pod, nil
}

func allow(uid types.UID) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     uid,
		Allowed: true,
	}
}

func allowPatched(uid types.UID, patch []byte) *admissionv1.AdmissionResponse {
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{
		UID:       uid,
		Allowed:   true,
		Patch:     patch,
		PatchType: &pt,
	}
}

func deny(uid types.UID, msg string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     uid,
		Allowed: false,
		Result:  &metav1.Status{Message: msg},
	}
}

func buildMutationPatch(pod *corev1.Pod, targetNode, podMoveName string) []map[string]interface{} {
	patch := buildNodeNamePatch(targetNode)
	patch = append(patch, buildLabelPatch(pod.Labels, podMoveName)...)
	return patch
}

func buildNodeNamePatch(targetNodeName string) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"op":    "add",
			"path":  "/spec/nodeName",
			"value": targetNodeName,
		},
	}
}

func buildLabelPatch(existing map[string]string, podMoveName string) []map[string]interface{} {
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
