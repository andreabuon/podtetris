package main

import (
	"os"
	"path/filepath"
	"testing"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCostMatcher_firstMatchWins(t *testing.T) {
	m, err := newCostMatcher(CostsFile{
		DefaultCost: 10,
		Rules: []CostRule{
			{
				Name: "db-stateful",
				Cost: 100,
				Match: MatchSpec{
					Namespaces: []string{"db"},
					Kinds:      []string{"StatefulSet"},
					NameRegex:  "^pg-.*",
				},
			},
			{
				Name: "any-in-db",
				Cost: 50,
				Match: MatchSpec{
					Namespaces: []string{"db"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("newCostMatcher: %v", err)
	}

	tests := []struct {
		name string
		pod  *apiv1.Pod
		want int
	}{
		{
			name: "stateful pg in db",
			pod:  podWithController("db", "pg-0", "StatefulSet", nil),
			want: 100,
		},
		{
			name: "other pod in db",
			pod:  podWithController("db", "api-0", "Deployment", nil),
			want: 50,
		},
		{
			name: "default",
			pod:  podWithController("app", "web-0", "Deployment", nil),
			want: 10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.getPodMovementCost(tt.pod)
			if err != nil {
				t.Fatalf("getPodMovementCost: %v", err)
			}
			if got != tt.want {
				t.Fatalf("getPodMovementCost = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCostMatcher_labelSelector(t *testing.T) {
	m, err := newCostMatcher(CostsFile{
		DefaultCost: 10,
		Rules: []CostRule{
			{
				Name: "tier-db",
				Cost: 80,
				Match: MatchSpec{
					LabelSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"tier": "database"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("newCostMatcher: %v", err)
	}

	got, err := m.getPodMovementCost(podWithController("ns", "x", "Deployment", map[string]string{"tier": "database"}))
	if err != nil {
		t.Fatalf("getPodMovementCost: %v", err)
	}
	if got != 80 {
		t.Fatalf("got %d, want 80", got)
	}
	got, err = m.getPodMovementCost(podWithController("ns", "x", "Deployment", map[string]string{"tier": "web"}))
	if err != nil {
		t.Fatalf("getPodMovementCost: %v", err)
	}
	if got != 10 {
		t.Fatalf("got %d, want 10", got)
	}
}

func TestNewCostMatcher_invalidRegex(t *testing.T) {
	_, err := newCostMatcher(CostsFile{
		Rules: []CostRule{{
			Name:  "bad",
			Match: MatchSpec{NameRegex: "("},
		}},
	})
	if err == nil {
		t.Fatal("expected error for invalid nameRegex")
	}
}

func TestLoadCostsConfig_viper(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "costs.yaml")
	content := []byte(`
defaultCost: 7
rules:
  - name: web
    cost: 3
    match:
      namespaces: [app]
      labelSelector:
        matchLabels:
          tier: frontend
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	m, err := loadCostsConfig()
	if err != nil {
		t.Fatalf("loadCostsConfig: %v", err)
	}
	if m.defaultCost != 7 {
		t.Fatalf("defaultCost = %d, want 7", m.defaultCost)
	}
	pod := podWithController("app", "web-0", "Deployment", map[string]string{"tier": "frontend"})
	got, err := m.getPodMovementCost(pod)
	if err != nil {
		t.Fatalf("getPodMovementCost: %v", err)
	}
	if got != 3 {
		t.Fatalf("getPodMovementCost = %d, want 3", got)
	}
}

func TestGetPodMovementCost_nilInvariants(t *testing.T) {
	var nilMatcher *CostMatcher
	if _, err := nilMatcher.getPodMovementCost(podWithController("ns", "p", "Deployment", nil)); err == nil {
		t.Fatal("expected error for nil matcher")
	}

	m, err := newCostMatcher(CostsFile{DefaultCost: 10})
	if err != nil {
		t.Fatalf("newCostMatcher: %v", err)
	}
	if _, err := m.getPodMovementCost(nil); err == nil {
		t.Fatal("expected error for nil pod")
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
