package main

import (
	"testing"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCostMatcher_namespaceRegex(t *testing.T) {
	m, err := newCostMatcher(CostsFile{
		DefaultCost: 10,
		Rules: []CostRule{
			{
				Name: "tenant-ns",
				Cost: 40,
				Match: MatchSpec{
					NamespaceRegex: "^tenant-.*",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("newCostMatcher: %v", err)
	}

	got, err := m.getPodMovementCost(podWithController("tenant-a", "web", "ReplicaSet", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got != 40 {
		t.Fatalf("got %d, want 40", got)
	}

	got, err = m.getPodMovementCost(podWithController("default", "web", "ReplicaSet", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("got %d, want 10", got)
	}
}

func TestCostMatcher_namespacesAndNamespaceRegexAND(t *testing.T) {
	m, err := newCostMatcher(CostsFile{
		DefaultCost: 10,
		Rules: []CostRule{{
			Name: "exact-and-regex",
			Cost: 70,
			Match: MatchSpec{
				Namespaces:     []string{"db", "db-prod"},
				NamespaceRegex: "^db",
			},
		}},
	})
	if err != nil {
		t.Fatalf("newCostMatcher: %v", err)
	}

	// in list and matches regex
	got, err := m.getPodMovementCost(podWithController("db-prod", "p", "StatefulSet", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got != 70 {
		t.Fatalf("got %d, want 70", got)
	}

	// matches regex but not in exact list
	got, err = m.getPodMovementCost(podWithController("db-staging", "p", "StatefulSet", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got != 10 {
		t.Fatalf("got %d, want 10", got)
	}
}

func TestNewCostMatcher_invalidNamespaceRegex(t *testing.T) {
	_, err := newCostMatcher(CostsFile{
		Rules: []CostRule{{
			Name:  "bad",
			Match: MatchSpec{NamespaceRegex: "("},
		}},
	})
	if err == nil {
		t.Fatal("expected error for invalid namespaceRegex")
	}
}

func podWithController(ns, name, kind string, labels map[string]string) *apiv1.Pod {
	return &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       kind,
				Name:       "owner",
				UID:        "uid",
				Controller: boolPtr(true),
			}},
		},
	}
}

func boolPtr(b bool) *bool { return &b }
