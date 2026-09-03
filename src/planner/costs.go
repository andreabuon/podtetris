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

// PodsSelector selects pods. All set fields are ANDed; omitted fields are ignored.
type PodsSelector struct {
	PodNameRegex   string                `mapstructure:"nameRegex"`
	Namespaces     []string              `mapstructure:"namespaces"`
	NamespaceRegex string                `mapstructure:"namespaceRegex"`
	Kinds          []string              `mapstructure:"kinds"` // controller OwnerReference.Kind
	LabelSelector  *metav1.LabelSelector `mapstructure:"labelSelector"`
}

type Rule struct {
	Name     string       `mapstructure:"name"`
	Selector PodsSelector `mapstructure:"selector"`
}

// FixedPodsRule specifies that a pod should not be moved across nodes
type FixedPodsRule struct {
	Rule `mapstructure:"rule, squash"`
}

// MoveCostRule assigns a cost to the movement of pods matching PodsSelector
type MoveCostRule struct {
	Rule `mapstructure:"rule, squash"`
	Cost int `mapstructure:"cost"`
}

type RulesFile struct {
	DefaultMoveCost int             `mapstructure:"defaultMoveCost"`
	FixedPodsRules  []FixedPodsRule `mapstructure:"fixedPodsRules"`
	MoveCostRules   []MoveCostRule  `mapstructure:"moveCostRules"`
}

type CompiledPodsSelector struct {
	podNameRegex   *regexp.Regexp
	namespaces     []string
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
	name     string
	selector CompiledPodsSelector
}

type compiledCostRule struct {
	compiledRule
	cost int
}

// ###

func (rm RuleMatch) String() string {
	return fmt.Sprintf("matched rule = %s", rm.matchedRule.Name)
}

func (moveCostRule MoveCostRuleMatch) String() string {
	return fmt.Sprintf("matched rule = %s, cost = %d", moveCostRule.MoveCostRule.Rule.Name, moveCostRule.MoveCostRule.Cost)
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

func (m *RuleMatcher) getPodMovementCost(pod *apiv1.Pod) (MoveCostRuleMatch, error) {
	if m == nil {
		return MoveCostRuleMatch{}, errors.New("cost matcher is nil")
	}
	if pod == nil {
		return MoveCostRuleMatch{}, errors.New("pod is nil")
	}

	for i, rule := range m.costRules {
		if rule.selector.matches(pod) {
			return MoveCostRuleMatch{}, nil //FIXME
		}
	}
	return MoveCostRuleMatch{}, nil //FIXME
}

func (selector CompiledPodsSelector) matches(pod *apiv1.Pod) bool {
	if selector.podNameRegex != nil && !selector.podNameRegex.MatchString(pod.Name) {
		return false
	}
	if len(selector.namespaces) > 0 && !slices.Contains(selector.namespaces, pod.Namespace) {
		return false
	}
	if selector.namespaceRegex != nil && !selector.namespaceRegex.MatchString(pod.Namespace) {
		return false
	}
	if len(selector.kinds) > 0 && !slices.Contains(selector.kinds, controllerKind(pod)) {
		return false
	}
	if selector.labelSelector != nil && !selector.labelSelector.Matches(labels.Set(pod.Labels)) {
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
