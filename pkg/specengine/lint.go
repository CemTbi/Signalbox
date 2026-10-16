// SPDX-License-Identifier: Apache-2.0

package specengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/daveshanley/vacuum/model"
	"github.com/daveshanley/vacuum/motor"
	"github.com/daveshanley/vacuum/rulesets"
)

// RuleSet names a group of lint rules.
type RuleSet string

const (
	// RuleSetRecommended checks naming, documentation and design quality.
	RuleSetRecommended RuleSet = "recommended"
	// RuleSetOWASP checks against the OWASP API Security Top 10 (2023).
	RuleSetOWASP RuleSet = "owasp"
	// RuleSetAll enables every built-in rule, including the OWASP rules.
	RuleSetAll RuleSet = "all"
)

// ParseRuleSets reads a comma-separated list such as "recommended,owasp".
// "none" and the empty string mean no lint rules.
func ParseRuleSets(list string) ([]RuleSet, error) {
	var sets []RuleSet
	seen := map[RuleSet]bool{}
	for _, part := range strings.Split(list, ",") {
		name := RuleSet(strings.ToLower(strings.TrimSpace(part)))
		switch name {
		case "", "none":
			continue
		case RuleSetRecommended, RuleSetOWASP, RuleSetAll:
			if !seen[name] {
				seen[name] = true
				sets = append(sets, name)
			}
		default:
			return nil, fmt.Errorf("unknown rule set %q: use recommended, owasp, all or none", part)
		}
	}
	return sets, nil
}

// Rules that re-check what Validate already reports. Running them would
// report every structural problem twice.
var coveredByValidate = map[string]bool{
	"oas2-schema": true,
	"oas3-schema": true,
}

// The engine injects reference problems as results with this ID. The
// resolver in Validate reports them with better messages and positions.
const injectedReferenceResult = "resolving-references"

// severityOverrides aligns rule severities with Spectral's defaults where
// vacuum is stricter. A missing description makes a spec incomplete, not
// invalid, so it shouldn't fail a build that gates on errors.
var severityOverrides = map[string]Severity{
	"info-description":      SeverityWarning,
	"operation-operationId": SeverityWarning,
}

var builtInRuleSets = sync.OnceValue(func() rulesets.RuleSets {
	return rulesets.BuildDefaultRuleSetsWithLogger(slog.New(slog.DiscardHandler))
})

// Lint runs the rules from the given rule sets and returns their findings.
// With no rule sets it does nothing. The error is reserved for failures of
// the linter itself.
func (d *Document) Lint(ctx context.Context, sets ...RuleSet) ([]Diagnostic, error) {
	if len(sets) == 0 {
		return nil, nil
	}
	rs, err := buildRuleSet(sets)
	if err != nil {
		return nil, err
	}

	exec := &motor.RuleSetExecution{
		RuleSet:           rs,
		Spec:              d.raw,
		SpecFileName:      d.opts.SourceName,
		Base:              d.opts.BaseDir,
		AllowLookup:       d.opts.AllowRemoteReferences,
		SilenceLogs:       true,
		Logger:            slog.New(slog.DiscardHandler),
		SkipSchemaErrors:  true,
		SkipCircularCheck: true,
	}
	res := motor.ApplyRulesToRuleSetWithOptions(exec, &motor.ExecutionOptions{Context: ctx})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("lint: the rule engine returned no result")
	}
	// The engine also reports document problems as errors. Validate covers
	// those, so they only matter when no rule could run at all.
	if len(res.Results) == 0 && len(res.Errors) > 0 && !documentProblemsOnly(res.Errors) {
		return nil, fmt.Errorf("lint: %w", errors.Join(res.Errors...))
	}

	diags := make([]Diagnostic, 0, len(res.Results))
	for _, r := range res.Results {
		if reportedByValidate(r) {
			continue
		}
		diags = append(diags, lintDiagnostic(r))
	}
	SortDiagnostics(diags)
	return dedupe(diags), nil
}

// reportedByValidate filters lint results that restate a reference problem
// the resolver already reported as an error.
func reportedByValidate(r model.RuleFunctionResult) bool {
	if r.RuleId == injectedReferenceResult {
		return true
	}
	// A $ref to a missing component also trips the unused-component rule,
	// with the same "does not exist" message.
	return r.RuleId == "oas3-unused-component" && strings.Contains(r.Message, "does not exist")
}

func buildRuleSet(sets []RuleSet) (*rulesets.RuleSet, error) {
	defaults := builtInRuleSets()
	merged := map[string]*model.Rule{}
	add := func(rules map[string]*model.Rule) {
		for id, r := range rules {
			if !coveredByValidate[id] {
				merged[id] = r
			}
		}
	}
	for _, s := range sets {
		switch s {
		case RuleSetRecommended:
			add(defaults.GenerateOpenAPIRecommendedRuleSet().Rules)
		case RuleSetOWASP:
			add(rulesets.GetAllOWASPRules())
		case RuleSetAll:
			add(rulesets.GetAllBuiltInRules())
			add(rulesets.GetAllOWASPRules())
		default:
			return nil, fmt.Errorf("unknown rule set %q", s)
		}
	}
	return rulesets.CreateRuleSetFromRuleMap(merged), nil
}

func documentProblemsOnly(errs []error) bool {
	for _, err := range errs {
		msg := err.Error()
		if !strings.Contains(msg, "reference") && !strings.Contains(msg, "unable to") {
			return false
		}
	}
	return true
}

func lintDiagnostic(r model.RuleFunctionResult) Diagnostic {
	ruleID, severity := r.RuleId, r.RuleSeverity
	if r.Rule != nil {
		ruleID = firstNonEmpty(ruleID, r.Rule.Id)
		severity = firstNonEmpty(severity, r.Rule.Severity)
	}
	d := Diagnostic{
		Severity: severityFromRule(ruleID, severity),
		Source:   SourceLinter,
		Code:     ruleID,
		Message:  sentence(r.Message),
		Path:     r.Path,
		Range:    lintRange(r),
	}
	if r.Rule != nil && r.Rule.HowToFix != "" {
		d.HowToFix = r.Rule.HowToFix
	}
	return d
}

// lintRange places a lint result. The engine only fills Range when it builds
// a report, so the nodes are the reliable source.
func lintRange(r model.RuleFunctionResult) Range {
	if r.Range.Start.Line > 0 {
		return Range{
			Start: Position{Line: r.Range.Start.Line, Column: r.Range.Start.Char},
			End:   Position{Line: r.Range.End.Line, Column: r.Range.End.Char},
		}
	}
	switch {
	case r.Origin != nil && r.Origin.Line > 0:
		return pointAt(r.Origin.Line, r.Origin.Column)
	case r.StartNode != nil:
		rng := pointAt(r.StartNode.Line, r.StartNode.Column)
		if r.EndNode != nil && r.EndNode.Line >= r.StartNode.Line {
			rng.End = Position{Line: r.EndNode.Line, Column: r.EndNode.Column}
		}
		return rng
	}
	return pointAt(0, 0)
}

func severityFromRule(ruleID, s string) Severity {
	if override, ok := severityOverrides[ruleID]; ok {
		return override
	}
	switch s {
	case model.SeverityError:
		return SeverityError
	case model.SeverityWarn:
		return SeverityWarning
	case model.SeverityInfo:
		return SeverityInfo
	default:
		return SeverityHint
	}
}
