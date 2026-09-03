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

// compiledRule is a rule with compiled regex ready for reuse.
type compiledRule struct {
	Name     string
	Selector CompiledPodsSelector
}

type compiledCostRule struct {
	compiledRule
	Cost int
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

// ###

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
		defaultCost:    file.DefaultMoveCost,
		fixedPodsRules: make([]compiledRule, 0, len(file.FixedPodsRules)),
		costRules:      make([]compiledCostRule, 0, len(file.MoveCostRules)),
	}

	// add fixed pods rules
	for i, rule := range file.FixedPodsRules {
		compiledSelector, err := rule.Selector.Compile()
		if err != nil {
			return nil, fmt.Errorf("fixed rule %d: %w", i, err)
		}

		fixedRule := compiledRule{
			Name:     rule.Name,
			Selector: compiledSelector,
		}

		m.fixedPodsRules = append(m.fixedPodsRules, fixedRule)
	}

	// add move cost rules
	for i, rule := range file.MoveCostRules {
		compiledSelector, err := rule.Selector.Compile()
		if err != nil {
			return nil, fmt.Errorf("cost rule %d: %w", i, err)
		}

		compiledRule := compiledRule{
			Name:     rule.Name,
			Selector: compiledSelector,
		}

		compiledCostRule := compiledCostRule{
			compiledRule,
			rule.Cost,
		}

		m.costRules = append(m.costRules, compiledCostRule)
	}
	return m, nil
}

func (selector *PodsSelector) Compile() (CompiledPodsSelector, error) {
	compiledSelector := CompiledPodsSelector{
		namespaces:    selector.Namespaces,
		kinds:         selector.Kinds,
		labelSelector: selector.LabelSelector,
	}

	if selector.PodNameRegex != "" {
		re, err := regexp.Compile(selector.PodNameRegex)
		if err != nil {
			return CompiledPodsSelector{}, fmt.Errorf("PodNameRegex: %w", err)
		}
		compiledSelector.podNameRegex = re
	}

	if selector.NamespaceRegex != "" {
		re, err := regexp.Compile(selector.NamespaceRegex)
		if err != nil {
			return CompiledPodsSelector{}, fmt.Errorf("NamespaceRegex: %w", err)
		}
		compiledSelector.namespaceRegex = re
	}

	if selector.LabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(selector.LabelSelector)
		if err != nil {
			return CompiledPodsSelector{}, fmt.Errorf("labelSelector: %w", err)
		}
		compiledSelector.labelSelector = sel
	}

	return compiledSelector, nil
}

func (m *RuleMatcher) getPodMovementCost(pod *apiv1.Pod) (int, error) {
	if m == nil {
		return 0, errors.New("cost matcher is nil")
	}
	if pod == nil {
		return 0, errors.New("pod is nil")
	}

	for _, rule := range m.costRules {
		if rule.Selector.matches(pod) {
			return rule.Cost, nil
		}
	}
	return m.defaultCost, nil
}

func (m *RuleMatcher) isFixed(pod *apiv1.Pod) (bool, error) {
	if m == nil {
		return false, errors.New("cost matcher is nil")
	}
	if pod == nil {
		return false, errors.New("pod is nil")
	}

	for _, rule := range m.fixedPodsRules {
		if rule.Selector.matches(pod) {
			return true, nil
		}
	}
	return false, nil
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
