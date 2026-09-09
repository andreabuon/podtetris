package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	skip := map[string]struct{}{}

	for attempt := 0; attempt < MAXCLAIMATTEMPS; attempt++ {
		var err error
		pm, err = findMatchingPodMove(ctx, &pod, skip)
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
			if errors.Is(err, errPodMoveAlreadyClaimed) || apierrors.IsConflict(err) {
				log.Printf("PodMove %s/%s unavailable (%v); looking for another open move",
					pm.Namespace, pm.Name, err)
				skip[pm.Name] = struct{}{}
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
