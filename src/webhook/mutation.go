package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	"go.uber.org/zap"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
		log.Error("Error writing response", zap.Error(err))
	}
}

// buildAdmissionResponse decides what patch (if any) to return for the incoming pod.
// Matching open PodMoves are claimed and the pod is pinned to their targetNode.
func buildAdmissionResponse(ctx context.Context, req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	pod, err := decodePod(req)
	if err != nil {
		log.Error("Error unmarshalling pod", zap.Error(err))
		return deny(req.UID, fmt.Sprintf("could not unmarshal pod: %v", err))
	}

	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return allow(req.UID)
	}

	matchingPodMoves, err := listOpenPodMoveMatches(ctx, pod)
	if err != nil {
		log.Error("Error listing open PodMoves",
			zap.String("namespace", pod.Namespace),
			zap.String("pod", podDisplayName(pod)),
			zap.Error(err),
		)
		return deny(req.UID, err.Error())
	}

	if len(matchingPodMoves) == 0 {
		return allow(req.UID)
	}

	var chosenPodMove *podtetrisiov1.PodMove
	for _, podMove := range matchingPodMoves {
		evicted, err := hasBeenEvicted(ctx, podMove)
		if err != nil {
			log.Info("Could not determine whether source pod has been evicted; trying next PodMove",
				zap.String("namespace", podMove.Spec.Pod.Namespace),
				zap.String("pod", podMove.Spec.Pod.Name),
				zap.Error(err),
			)
			continue
		}

		if !evicted {
			continue
		}

		claimed, err := claimReplacement(ctx, podMove, pod)
		if err != nil {
			log.Error("Error claiming PodMove",
				zap.String("namespace", pod.Namespace),
				zap.String("pod", podDisplayName(pod)),
				zap.Error(err),
			)
			return deny(req.UID, err.Error())
		}
		if !claimed {
			log.Info("PodMove no longer open; trying next candidate",
				zap.String("namespace", podMove.Namespace),
				zap.String("podMove", podMove.Name),
			)
			continue
		}

		log.Info("Intercepted CREATE; pinning pod to target node",
			zap.String("namespace", pod.Namespace),
			zap.String("pod", podDisplayName(pod)),
			zap.String("generateName", pod.GenerateName),
			zap.String("targetNode", podMove.Spec.TargetNode),
			zap.String("podMoveNamespace", podMove.Namespace),
			zap.String("podMove", podMove.Name),
		)
		chosenPodMove = podMove
		break
	}

	if chosenPodMove == nil {
		return allow(req.UID)
	}

	patchBytes, err := json.Marshal(buildMutationPatch(pod, chosenPodMove.Spec.TargetNode, chosenPodMove.Name))
	if err != nil {
		log.Error("Error marshalling patch", zap.Error(err))
		return deny(req.UID, fmt.Sprintf("could not marshal patch: %v", err))
	}
	return allowPatched(req.UID, patchBytes)
}

// hasBeenEvicted reports whether the PodMove's source pod is gone (or already marked SourceEvicted).
func hasBeenEvicted(ctx context.Context, pm *podtetrisiov1.PodMove) (bool, error) {
	if meta.IsStatusConditionTrue(pm.Status.Conditions, podtetrisiov1.ConditionSourceEvicted) {
		return true, nil
	}

	ref := pm.Spec.Pod
	evicted, err := isSourcePodGone(ctx, cacheReader, ref)
	if err != nil {
		return false, err
	}
	if evicted {
		return true, nil
	}
	return isSourcePodGone(ctx, apiClient, ref)
}

func isSourcePodGone(ctx context.Context, r client.Reader, ref corev1.ObjectReference) (bool, error) {
	var retrieved corev1.Pod
	err := r.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &retrieved)

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
