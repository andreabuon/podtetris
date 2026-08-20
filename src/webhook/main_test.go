package main

import (
	"context"
	"errors"
	"testing"

	podtetrisiov1 "github.com/andreabuon/podtetris/src/evictor/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsOpenForReplacement(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{name: "no status", want: false},
		{
			name:       "evicting",
			conditions: []metav1.Condition{cond(podtetrisiov1.ConditionEvicted, metav1.ConditionFalse)},
			want:       true,
		},
		{
			name:       "evicted",
			conditions: []metav1.Condition{cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue)},
			want:       true,
		},
		{
			name: "already recreated",
			conditions: []metav1.Condition{
				cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue),
				cond(podtetrisiov1.ConditionTargetNodeInjected, metav1.ConditionTrue),
			},
			want: false,
		},
		{
			name: "injected false still open",
			conditions: []metav1.Condition{
				cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue),
				cond(podtetrisiov1.ConditionTargetNodeInjected, metav1.ConditionFalse),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := &podtetrisiov1.PodMove{Status: podtetrisiov1.PodMoveStatus{Conditions: tt.conditions}}
			if got := isOpenForReplacement(pm); got != tt.want {
				t.Fatalf("isOpenForReplacement() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyTargetNodeInjected(t *testing.T) {
	pm := openPodMove("default", "move-1", owner("ReplicaSet", "nginx", "rs-uid"), "nginx-abc", "node-b")
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "nginx-abc", Namespace: "default"}}

	applyTargetNodeInjected(pm, pod)

	if isOpenForReplacement(pm) {
		t.Fatal("PodMove should be closed after TargetNodeInjected=True")
	}

	injected := meta.FindStatusCondition(pm.Status.Conditions, podtetrisiov1.ConditionTargetNodeInjected)
	if injected == nil {
		t.Fatal("TargetNodeInjected condition not found")
	}
	if injected.Status != metav1.ConditionTrue {
		t.Fatalf("status = %v, want True", injected.Status)
	}
	if injected.Reason != conditionReasonReplacementCreated {
		t.Fatalf("reason = %v, want %s", injected.Reason, conditionReasonReplacementCreated)
	}
	if injected.LastTransitionTime.IsZero() {
		t.Fatal("lastTransitionTime must record when the replacement was recreated")
	}
	if injected.Message != `Replacement pod default/nginx-abc recreated and pinned to node "node-b"` {
		t.Fatalf("message = %q", injected.Message)
	}
	if meta.FindStatusCondition(pm.Status.Conditions, podtetrisiov1.ConditionEvicted) == nil {
		t.Fatal("existing Evicted condition must be preserved")
	}
	if pm.Status.Phase != podtetrisiov1.PodMovePhaseVerifying {
		t.Fatalf("phase = %q, want %q", pm.Status.Phase, podtetrisiov1.PodMovePhaseVerifying)
	}
}

func TestClaimReplacement(t *testing.T) {
	pm := openPodMove("default", "move-1", owner("ReplicaSet", "nginx", "rs-uid"), "nginx-abc", "node-b")
	k8sClient = newFakeClient(pm)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "nginx-abc", Namespace: "default"}}
	if err := claimReplacement(context.Background(), pm.DeepCopy(), pod); err != nil {
		t.Fatalf("claimReplacement() first call: %v", err)
	}

	got := &podtetrisiov1.PodMove{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "move-1"}, got); err != nil {
		t.Fatal(err)
	}
	if isOpenForReplacement(got) {
		t.Fatal("claimed PodMove should not stay open")
	}
	if got.Status.Phase != podtetrisiov1.PodMovePhaseVerifying {
		t.Fatalf("phase = %q, want %q", got.Status.Phase, podtetrisiov1.PodMovePhaseVerifying)
	}

	err := claimReplacement(context.Background(), got.DeepCopy(), pod)
	if !errors.Is(err, errPodMoveAlreadyClaimed) {
		t.Fatalf("second claimReplacement() err = %v, want errPodMoveAlreadyClaimed", err)
	}
}

func TestReplacementMatches(t *testing.T) {
	rsOwner := owner("ReplicaSet", "nginx", "rs-uid")
	deployOwner := owner("Deployment", "nginx", "deploy-uid")
	stsOwner := owner("StatefulSet", "web", "sts-uid")

	rsMove := openPodMove("default", "move-rs", rsOwner, "nginx-old", "node-a")
	deployMove := openPodMove("default", "move-deploy", deployOwner, "nginx-old", "node-a")
	stsMove := openPodMove("default", "move-sts", stsOwner, "web-1", "node-b")

	if !replacementMatches(rsMove, ownedPod("", rsOwner), &rsOwner) {
		t.Fatal("ReplicaSet replacement should match by owner even with a new/empty pod name")
	}
	otherRS := owner("ReplicaSet", "other", "other-uid")
	if replacementMatches(rsMove, ownedPod("nginx-xyz", otherRS), &otherRS) {
		t.Fatal("ReplicaSet replacement should not match a different owner")
	}

	if !replacementMatches(deployMove, ownedPod("", deployOwner), &deployOwner) {
		t.Fatal("Deployment replacement should match by owner")
	}

	if !replacementMatches(stsMove, ownedPod("web-1", stsOwner), &stsOwner) {
		t.Fatal("StatefulSet replacement should match owner+pod name")
	}
	if replacementMatches(stsMove, ownedPod("web-2", stsOwner), &stsOwner) {
		t.Fatal("StatefulSet replacement should not match a different ordinal")
	}
	if replacementMatches(stsMove, ownedPod("", stsOwner), &stsOwner) {
		t.Fatal("StatefulSet replacement with empty name should not match")
	}
}

func TestFindMatchingPodMoveReplicaSetByOwner(t *testing.T) {
	podtetrisNamespace = "podtetris"
	rsOwner := owner("ReplicaSet", "nginx", "rs-uid")
	moveA := openPodMove("podtetris", "move-a", rsOwner, "nginx-old", "node-a")
	moveB := openPodMove("podtetris", "move-b", owner("ReplicaSet", "other", "other-uid"), "other-old", "node-c")
	k8sClient = newFakeClient(moveA, moveB)

	pod := ownedPod("", rsOwner)
	pod.GenerateName = "nginx-"
	got, err := findMatchingPodMove(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected a matching PodMove for the ReplicaSet owner")
	}
	if got.Name != "move-a" || got.Spec.TargetNode != "node-a" {
		t.Fatalf("got %s target=%s, want move-a/node-a", got.Name, got.Spec.TargetNode)
	}
}

func TestFindMatchingPodMoveStatefulSetByOwnerAndName(t *testing.T) {
	podtetrisNamespace = "podtetris"
	stsOwner := owner("StatefulSet", "web", "sts-uid")
	move0 := openPodMove("podtetris", "move-0", stsOwner, "web-0", "node-a")
	move1 := openPodMove("podtetris", "move-1", stsOwner, "web-1", "node-b")
	k8sClient = newFakeClient(move0, move1)

	got, err := findMatchingPodMove(context.Background(), ownedPod("web-1", stsOwner))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected a matching PodMove for web-1")
	}
	if got.Name != "move-1" || got.Spec.TargetNode != "node-b" {
		t.Fatalf("got %s target=%s, want move-1/node-b", got.Name, got.Spec.TargetNode)
	}

	got, err = findMatchingPodMove(context.Background(), ownedPod("web-2", stsOwner))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("web-2 should not claim a different ordinal, got %s", got.Name)
	}
}

func cond(condType string, status metav1.ConditionStatus) metav1.Condition {
	return metav1.Condition{Type: condType, Status: status}
}

func owner(kind, name string, uid types.UID) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       kind,
		Name:       name,
		UID:        uid,
		Controller: boolPtr(true),
	}
}

func ownedPod(name string, owner metav1.OwnerReference) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:            name,
		Namespace:       "default",
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
}

func openPodMove(namespace, name string, owner metav1.OwnerReference, podName, targetNode string) *podtetrisiov1.PodMove {
	return &podtetrisiov1.PodMove{
		TypeMeta: metav1.TypeMeta{
			APIVersion: podtetrisiov1.SchemeGroupVersion.String(),
			Kind:       "PodMove",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: podtetrisiov1.PodMoveSpec{
			Owner: owner,
			Pod: corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Pod",
				Namespace:  namespace,
				Name:       podName,
			},
			TargetNode: targetNode,
		},
		Status: podtetrisiov1.PodMoveStatus{
			Conditions: []metav1.Condition{
				cond(podtetrisiov1.ConditionEvicted, metav1.ConditionTrue),
			},
		},
	}
}

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	if err := podtetrisiov1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&podtetrisiov1.PodMove{}).
		WithObjects(objs...).
		Build()
}

func boolPtr(v bool) *bool { return &v }
