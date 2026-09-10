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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		log.Printf("no owner controller found for the pod. Allowing")
		return allow(req.UID)
	}

	matchingPodMoves, err := listOpenPodMoveMatches(ctx, pod)
	if err != nil {
		log.Printf("Error listing open PodMoves for pod %s/%s: %v", pod.Namespace, podDisplayName(pod), err)
		return deny(req.UID, err.Error())
	}

	if len(matchingPodMoves) == 0 {
		log.Printf("No matching PodMove for pod %s/%s; allowing without mutation",
			pod.Namespace, podDisplayName(pod))
		return allow(req.UID)
	}

	var chosenPodMove *podtetrisiov1.PodMove = nil
	for _, podMove := range matchingPodMoves {
		evicted, err := hasBeenEvicted(ctx, podMove.Spec.Pod)
		if err != nil {
			log.Printf("Can not determine whether the pod %s/%s has been evicted: %v. Trying the next PodMove", podMove.Spec.Pod.Namespace, podMove.Spec.Pod.Name, err)
			continue
		}

		if !evicted {
			continue
		}

		claimed, err := claimReplacement(ctx, &podMove, pod)
		if err != nil {
			log.Printf("Error claiming PodMove for pod %s/%s: %v", pod.Namespace, podDisplayName(pod), err)
			return deny(req.UID, err.Error())
		}
		if !claimed {
			log.Printf("PodMove %s/%s no longer open; trying next candidate", podMove.Namespace, podMove.Name)
			continue
		}

		log.Printf("Intercepted CREATE for pod %s/%s (generateName=%q) -> pinning to node %q from PodMove %s/%s", pod.Namespace, podDisplayName(pod), pod.GenerateName, podMove.Spec.TargetNode, podMove.Namespace, podMove.Name)
		chosenPodMove = &podMove
		break
	}

	if chosenPodMove == nil {
		log.Printf("no possible open PodMove could be chosen. Allowing with no mutation")
		return allow(req.UID)
	}

	patchBytes, err := json.Marshal(buildMutationPatch(pod, chosenPodMove.Spec.TargetNode, chosenPodMove.Name))
	if err != nil {
		log.Printf("Error marshalling patch: %v", err)
		return deny(req.UID, fmt.Sprintf("could not marshal patch: %v", err))
	}
	return allowPatched(req.UID, patchBytes)
}

func hasBeenEvicted(ctx context.Context, ref corev1.ObjectReference) (bool, error) {
	var retrieved corev1.Pod
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &retrieved)

	switch {
	case apierrors.IsNotFound(err):
		return true, nil
	case err != nil:
		return false, err
	case ref.UID != "" && retrieved.UID != ref.UID:
		return true, nil
	case !retrieved.DeletionTimestamp.IsZero():
		return true, nil
	default:
		return false, nil
	}
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
