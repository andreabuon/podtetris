package main

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/spf13/viper"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type CostsFile struct {
	DefaultCost int        `mapstructure:"defaultCost"`
	Rules       []CostRule `mapstructure:"rules"`
}

// CostRule assigns a move cost to pods matching Match.
type CostRule struct {
	Name  string    `mapstructure:"name"`
	Cost  int       `mapstructure:"cost"`
	Match MatchSpec `mapstructure:"match"`
}

// MatchSpec filters pods. It evalutes all set fields, ANDed. Omitted fields are ignored.
type MatchSpec struct {
	Namespaces    []string              `mapstructure:"namespaces"`
	Kinds         []string              `mapstructure:"kinds"` // controller OwnerReference.Kind
	NameRegex     string                `mapstructure:"nameRegex"`
	LabelSelector *metav1.LabelSelector `mapstructure:"labelSelector"`
}

// preparedRule is a CostRule with regex/label selector ready for repeated matching.
type preparedRule struct {
	CostRule
	nameRegex     *regexp.Regexp
	labelSelector labels.Selector
}

// CostMatcher resolves pod move costs from ordered rules (first match wins).
type CostMatcher struct {
	defaultCost int
	rules       []preparedRule
}

func loadCostsConfig() (*CostMatcher, error) {
	v := viper.New()
	v.SetConfigName("costs")
	v.AddConfigPath("/etc/podtetris/")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read costs config: %w", err)
	}

	var file CostsFile
	if err := v.Unmarshal(&file); err != nil {
		return nil, fmt.Errorf("unmarshal costs config: %w", err)
	}
	return newCostMatcher(file)
}

func newCostMatcher(file CostsFile) (*CostMatcher, error) {
	m := &CostMatcher{defaultCost: file.DefaultCost}

	for index, rule := range file.Rules {
		prepared, err := prepareRule(rule)
		if err != nil {
			name := rule.Name
			if name == "" {
				name = fmt.Sprintf("rule #%d", index)
			}
			return nil, fmt.Errorf("cost rule %q: %w", name, err)
		}
		m.rules = append(m.rules, prepared)
	}
	return m, nil
}

func prepareRule(rule CostRule) (preparedRule, error) {
	p := preparedRule{CostRule: rule}

	if rule.Match.NameRegex != "" {
		re, err := regexp.Compile(rule.Match.NameRegex)
		if err != nil {
			return preparedRule{}, fmt.Errorf("nameRegex: %w", err)
		}
		p.nameRegex = re
	}

	if rule.Match.LabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(rule.Match.LabelSelector)
		if err != nil {
			return preparedRule{}, fmt.Errorf("labelSelector: %w", err)
		}
		p.labelSelector = sel
	}
	return p, nil
}

func (m *CostMatcher) getPodMovementCost(pod *apiv1.Pod) (int, error) {
	if m == nil {
		return 0, errors.New("cost matcher is nil")
	}
	if pod == nil {
		return 0, errors.New("pod is nil")
	}
	for _, rule := range m.rules {
		if rule.matches(pod) {
			return rule.Cost, nil
		}
	}
	return m.defaultCost, nil
}

func (r preparedRule) matches(pod *apiv1.Pod) bool {
	if len(r.Match.Namespaces) > 0 && !slices.Contains(r.Match.Namespaces, pod.Namespace) {
		return false
	}
	if len(r.Match.Kinds) > 0 && !slices.Contains(r.Match.Kinds, controllerKind(pod)) {
		return false
	}
	if r.nameRegex != nil && !r.nameRegex.MatchString(pod.Name) {
		return false
	}
	if r.labelSelector != nil && !r.labelSelector.Matches(labels.Set(pod.Labels)) {
		return false
	}
	return true
}

func controllerKind(pod *apiv1.Pod) string {
	if ref := metav1.GetControllerOf(pod); ref != nil {
		return ref.Kind
	}
	return ""
}
