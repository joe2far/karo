package eval

import (
	"context"
	"regexp"
	"strings"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RunInput contains the inputs for an eval run.
type RunInput struct {
	TaskTitle          string
	TaskType           string
	AcceptanceCriteria []string
	ResultArtifact     string
}

// RunResult contains the results of an eval run.
type RunResult struct {
	PassRate     float64
	FailureNotes string
	TotalCases   int32
	PassedCases  int32
	FailedCases  int32
}

// Runner executes eval cases against task output.
type Runner struct {
	client    client.Client
	evalSuite karov1alpha1.EvalSuite
}

// NewRunner creates a new eval runner.
func NewRunner(c client.Client, suite karov1alpha1.EvalSuite) *Runner {
	return &Runner{
		client:    c,
		evalSuite: suite,
	}
}

// Run executes all eval cases and returns the aggregate result.
func (r *Runner) Run(_ context.Context, input RunInput) (*RunResult, error) {
	total := int32(len(r.evalSuite.Spec.EvalCases))
	if total == 0 {
		return &RunResult{PassRate: 1.0, TotalCases: 0}, nil
	}

	var passed int32
	var failureNotes []string

	for _, ec := range r.evalSuite.Spec.EvalCases {
		casePassed := true
		for _, assertion := range ec.Assertions {
			if !evaluateAssertion(assertion, input.ResultArtifact) {
				casePassed = false
				failureNotes = append(failureNotes, "Case "+ec.ID+": assertion "+string(assertion.Type)+" failed")
				break
			}
		}
		if casePassed {
			passed++
		}
	}

	passRate := float64(passed) / float64(total)
	return &RunResult{
		PassRate:     passRate,
		TotalCases:   total,
		PassedCases:  passed,
		FailedCases:  total - passed,
		FailureNotes: strings.Join(failureNotes, "; "),
	}, nil
}

func evaluateAssertion(assertion karov1alpha1.Assertion, artifact string) bool {
	switch assertion.Type {
	case karov1alpha1.AssertionTypeContains:
		return strings.Contains(artifact, assertion.Value)
	case karov1alpha1.AssertionTypeNotContains:
		return !strings.Contains(artifact, assertion.Value)
	case karov1alpha1.AssertionTypeMatchesPattern:
		matched, err := regexp.MatchString(assertion.Pattern, artifact)
		return err == nil && matched
	case karov1alpha1.AssertionTypeNotMatchesPattern:
		matched, err := regexp.MatchString(assertion.Pattern, artifact)
		return err == nil && !matched
	case karov1alpha1.AssertionTypeLLMJudge:
		// LLM judge requires an actual LLM call — stub returns true for now
		return true
	default:
		return false
	}
}
