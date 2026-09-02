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

const defaultCostRuleName = "default-cost"

// CostsFile is the on-disk costs.yaml schema.
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

// MatchSpec filters pods. All set fields are ANDed; omitted fields are ignored.
type MatchSpec struct {
	NameRegex      string                `mapstructure:"nameRegex"`
	Namespaces     []string              `mapstructure:"namespaces"`
	NamespaceRegex string                `mapstructure:"namespaceRegex"`
	Kinds          []string              `mapstructure:"kinds"` // controller OwnerReference.Kind
	LabelSelector  *metav1.LabelSelector `mapstructure:"labelSelector"`
}

// RuleMatch is the cost rule applied to a pod move.
// Name is "default" when no configured rule matched.
type RuleMatch struct {
	Name string
	Cost int
}

func (rm RuleMatch) String() string {
	return fmt.Sprintf("rule = %s, cost = %d", rm.Name, rm.Cost)
}

// CostMatcher resolves pod move costs from ordered rules (first match wins).
type CostMatcher struct {
	defaultCost int
	rules       []preparedRule
}

// preparedRule is a CostRule with compiled matchers ready for reuse.
type preparedRule struct {
	CostRule
	nameRegex      *regexp.Regexp
	namespaceRegex *regexp.Regexp
	labelSelector  labels.Selector
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
	m := &CostMatcher{
		defaultCost: file.DefaultCost,
		rules:       make([]preparedRule, 0, len(file.Rules)),
	}

	for i, rule := range file.Rules {
		prepared, err := prepareRule(rule)
		if err != nil {
			return nil, fmt.Errorf("cost rule %q: %w", costRuleName(rule.Name, i), err)
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

	if rule.Match.NamespaceRegex != "" {
		re, err := regexp.Compile(rule.Match.NamespaceRegex)
		if err != nil {
			return preparedRule{}, fmt.Errorf("namespaceRegex: %w", err)
		}
		p.namespaceRegex = re
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

func (m *CostMatcher) getPodMovementCost(pod *apiv1.Pod) (RuleMatch, error) {
	if m == nil {
		return RuleMatch{}, errors.New("cost matcher is nil")
	}
	if pod == nil {
		return RuleMatch{}, errors.New("pod is nil")
	}

	for i, rule := range m.rules {
		if rule.matches(pod) {
			return RuleMatch{Name: costRuleName(rule.Name, i), Cost: rule.Cost}, nil
		}
	}
	return RuleMatch{Name: defaultCostRuleName, Cost: m.defaultCost}, nil
}

func (r preparedRule) matches(pod *apiv1.Pod) bool {
	if r.nameRegex != nil && !r.nameRegex.MatchString(pod.Name) {
		return false
	}
	if len(r.Match.Namespaces) > 0 && !slices.Contains(r.Match.Namespaces, pod.Namespace) {
		return false
	}
	if r.namespaceRegex != nil && !r.namespaceRegex.MatchString(pod.Namespace) {
		return false
	}
	if len(r.Match.Kinds) > 0 && !slices.Contains(r.Match.Kinds, controllerKind(pod)) {
		return false
	}
	if r.labelSelector != nil && !r.labelSelector.Matches(labels.Set(pod.Labels)) {
		return false
	}
	return true
}

func costRuleName(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("rule #%d", index)
}

func controllerKind(pod *apiv1.Pod) string {
	if ref := metav1.GetControllerOf(pod); ref != nil {
		return ref.Kind
	}
	return ""
}
