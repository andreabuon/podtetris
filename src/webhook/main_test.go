package main

import (
	"context"
	"errors"
	"testing"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestIsOpenForReplacement(t *testing.T) {
	tests := []struct {
		name       string
		conditions []interface{}
		want       bool
	}{
		{name: "no status", want: false},
		{
			name:       "evicting",
			conditions: []interface{}{cond(podtetrisiov1.ConditionEvicted, metav1.ConditionFalse)},
			want:       true,
		},
		{
			name:       "evicted",
			conditions: []interface{}{cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue)},
			want:       true,
		},
		{
			name: "already recreated",
			conditions: []interface{}{
				cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue),
				cond(podtetrisiov1.ConditionTargetNodeInjected, metav1.ConditionTrue),
			},
			want: false,
		},
		{
			name: "injected false still open",
			conditions: []interface{}{
				cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue),
				cond(podtetrisiov1.ConditionTargetNodeInjected, metav1.ConditionFalse),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := &unstructured.Unstructured{Object: map[string]interface{}{}}
			if tt.conditions != nil {
				if err := unstructured.SetNestedSlice(pm.Object, tt.conditions, "status", "conditions"); err != nil {
					t.Fatal(err)
				}
			}
			if got := isOpenForReplacement(pm); got != tt.want {
				t.Fatalf("isOpenForReplacement() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyTargetNodeInjected(t *testing.T) {
	pm := openPodMove("default", "move-1")
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx-abc", Namespace: "default"},
	}

	applyTargetNodeInjected(pm, pod, "node-b")

	if isOpenForReplacement(pm) {
		t.Fatal("PodMove should be closed after TargetNodeInjected=True")
	}

	conditions, found, err := unstructured.NestedSlice(pm.Object, "status", "conditions")
	if err != nil || !found {
		t.Fatalf("conditions missing: found=%v err=%v", found, err)
	}

	var injected map[string]interface{}
	for _, raw := range conditions {
		c := raw.(map[string]interface{})
		if c["type"] == podtetrisiov1.ConditionTargetNodeInjected {
			injected = c
			break
		}
	}
	if injected == nil {
		t.Fatal("TargetNodeInjected condition not found")
	}
	if injected["status"] != string(metav1.ConditionTrue) {
		t.Fatalf("status = %v, want True", injected["status"])
	}
	if injected["reason"] != conditionReasonReplacementCreated {
		t.Fatalf("reason = %v, want %s", injected["reason"], conditionReasonReplacementCreated)
	}
	if injected["lastTransitionTime"] == nil || injected["lastTransitionTime"] == "" {
		t.Fatal("lastTransitionTime must record when the replacement was recreated")
	}
	if injected["message"] != `Replacement pod default/nginx-abc recreated and pinned to node "node-b"` {
		t.Fatalf("message = %q", injected["message"])
	}

	evictedStillThere := false
	for _, raw := range conditions {
		c := raw.(map[string]interface{})
		if c["type"] == podtetrisiov1.ConditionEvicted {
			evictedStillThere = true
		}
	}
	if !evictedStillThere {
		t.Fatal("existing Evicted condition must be preserved")
	}
}

func TestClaimReplacement(t *testing.T) {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(podtetrisiov1.SchemeGroupVersion, &podtetrisiov1.PodMove{}, &podtetrisiov1.PodMoveList{})

	pm := openPodMove("default", "move-1")
	dynClient = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		podMoveGVR: "PodMoveList",
	}, pm)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "nginx-abc", Namespace: "default"}}
	if err := claimReplacement(context.Background(), pm.DeepCopy(), pod, "node-b"); err != nil {
		t.Fatalf("claimReplacement() first call: %v", err)
	}

	got, err := dynClient.Resource(podMoveGVR).Namespace("default").Get(context.Background(), "move-1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if isOpenForReplacement(got) {
		t.Fatal("claimed PodMove should not stay open")
	}

	err = claimReplacement(context.Background(), got.DeepCopy(), pod, "node-b")
	if !errors.Is(err, errPodMoveAlreadyClaimed) {
		t.Fatalf("second claimReplacement() err = %v, want errPodMoveAlreadyClaimed", err)
	}
}

func cond(condType string, status metav1.ConditionStatus) map[string]interface{} {
	return map[string]interface{}{
		"type":   condType,
		"status": string(status),
	}
}

func openPodMove(namespace, name string) *unstructured.Unstructured {
	pm := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "podtetris.io.podtetris.io/v1",
		"kind":       "PodMove",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"targetNode": "node-b",
		},
	}}
	_ = unstructured.SetNestedSlice(pm.Object, []interface{}{
		cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue),
	}, "status", "conditions")
	return pm
}
