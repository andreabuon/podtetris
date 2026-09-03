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

type RulesFile struct {
	DefaultMoveCost int             `mapstructure:"defaultMoveCost"`
	FixedPodsRules  []FixedPodsRule `mapstructure:"fixedPodsRules"`
	MoveCostRules   []MoveCostRule  `mapstructure:"moveCostRules"`
}

type Rule struct {
	RuleName     string       `mapstructure:"ruleName"`
	PodsSelector PodsSelector `mapstructure:"podsSelector"`
}

// PodsSelector selects pods. All set fields are ANDed; omitted fields are ignored.
type PodsSelector struct {
	PodNameRegex   string                `mapstructure:"nameRegex"`
	Namespaces     []string              `mapstructure:"namespaces"`
	NamespaceRegex string                `mapstructure:"namespaceRegex"`
	Kinds          []string              `mapstructure:"kinds"` // controller OwnerReference.Kind
	LabelSelector  *metav1.LabelSelector `mapstructure:"labelSelector"`
}

// FixedPodsRule specifies that a pod should not be moved across nodes
type FixedPodsRule struct {
	Rule Rule `mapstructure:"rule"`
}

// MoveCostRule assigns a cost to the movement of pods matching PodsSelector
type MoveCostRule struct {
	Rule Rule `mapstructure:"rule"`
	Cost int  `mapstructure:"cost"`
}

type CompiledPodsSelector struct {
	selector       *PodsSelector
	podNameRegex   *regexp.Regexp
	namespaces     string
	namespaceRegex *regexp.Regexp
	kinds          []string
	labelSelector  labels.Selector
}

type RuleMatcher struct {
	defaultCost    int
	fixedPodsRules []compiledRule
	costRules      []compiledCostRule
}

type RuleMatch struct {
	matchedRule *Rule
}

type MoveCostRuleMatch struct {
	MoveCostRule *MoveCostRule
}

// compiledRule is a rule with compiled regex ready for reuse.
type compiledRule struct {
	ruleName string
	selector *CompiledPodsSelector
}

type compiledCostRule struct {
	rule *compiledRule
	cost int
}

// ###

func (rm RuleMatch) String() string {
	return fmt.Sprintf("matched rule = %s", rm.matchedRule.RuleName)
}

func (moveCostRule MoveCostRuleMatch) String() string {
	return fmt.Sprintf("matched rule = %s, cost = %d", moveCostRule.MoveCostRule.Rule.RuleName, moveCostRule.MoveCostRule.Cost)
}

func loadCostsConfig() (*RuleMatcher, error) {
	v := viper.New()
	v.SetConfigName("costs")
	v.AddConfigPath("/etc/podtetris/")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read costs config: %w", err)
	}

	var file RulesFile
	if err := v.Unmarshal(&file); err != nil {
		return nil, fmt.Errorf("unmarshal costs config: %w", err)
	}
	return newCostMatcher(file)
}

func newCostMatcher(file RulesFile) (*RuleMatcher, error) {
	m := &RuleMatcher{
		defaultCost: file.DefaultMoveCost,
		rules:       make([]preparedRule, 0, len(file.MoveCostRules)),
	}

	for i, rule := range file.MoveCostRules {
		prepared, err := prepareRule(rule)
		if err != nil {
			return nil, fmt.Errorf("cost rule %q: %w", costRuleName(rule.RuleName, i), err)
		}
		m.rules = append(m.rules, prepared)
	}
	return m, nil
}

func prepareRule(rule MoveCostRule) (preparedRule, error) {
	p := preparedRule{MoveCostRule: rule}

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

func (m *RuleMatcher) getPodMovementCost(pod *apiv1.Pod) (RuleMatch, error) {
	if m == nil {
		return RuleMatch{}, errors.New("cost matcher is nil")
	}
	if pod == nil {
		return RuleMatch{}, errors.New("pod is nil")
	}

	for i, rule := range m.rules {
		if rule.matches(pod) {
			return RuleMatch{Name: costRuleName(rule.RuleName, i), Cost: rule.Cost}, nil
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
