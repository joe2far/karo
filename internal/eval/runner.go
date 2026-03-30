package eval

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	karov1alpha1 "github.com/joe2far/karo/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
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

// LLMCaller abstracts the LLM API call for testability.
type LLMCaller interface {
	Judge(ctx context.Context, provider, model, apiKey, criteria, artifact string) (bool, error)
}

// Runner executes eval cases against task output.
type Runner struct {
	client    client.Client
	evalSuite karov1alpha1.EvalSuite
	llmCaller LLMCaller
	namespace string
}

// NewRunner creates a new eval runner.
func NewRunner(c client.Client, suite karov1alpha1.EvalSuite) *Runner {
	return &Runner{
		client:    c,
		evalSuite: suite,
		llmCaller: &HTTPLLMCaller{},
		namespace: suite.Namespace,
	}
}

// Run executes all eval cases and returns the aggregate result.
func (r *Runner) Run(ctx context.Context, input RunInput) (*RunResult, error) {
	total := int32(len(r.evalSuite.Spec.EvalCases))
	if total == 0 {
		return &RunResult{PassRate: 1.0, TotalCases: 0}, nil
	}

	var passed int32
	var failureNotes []string

	for _, ec := range r.evalSuite.Spec.EvalCases {
		casePassed := true
		for _, assertion := range ec.Assertions {
			ok, err := r.evaluateAssertion(ctx, assertion, input.ResultArtifact)
			if err != nil {
				failureNotes = append(failureNotes, fmt.Sprintf("Case %s: %v", ec.ID, err))
				casePassed = false
				break
			}
			if !ok {
				failureNotes = append(failureNotes, fmt.Sprintf("Case %s: assertion %s failed", ec.ID, assertion.Type))
				casePassed = false
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

func (r *Runner) evaluateAssertion(ctx context.Context, assertion karov1alpha1.Assertion, artifact string) (bool, error) {
	switch assertion.Type {
	case karov1alpha1.AssertionTypeContains:
		return strings.Contains(artifact, assertion.Value), nil
	case karov1alpha1.AssertionTypeNotContains:
		return !strings.Contains(artifact, assertion.Value), nil
	case karov1alpha1.AssertionTypeMatchesPattern:
		matched, err := regexp.MatchString(assertion.Pattern, artifact)
		return err == nil && matched, err
	case karov1alpha1.AssertionTypeNotMatchesPattern:
		matched, err := regexp.MatchString(assertion.Pattern, artifact)
		return err == nil && !matched, err
	case karov1alpha1.AssertionTypeLLMJudge:
		return r.evaluateLLMJudge(ctx, assertion, artifact)
	default:
		return false, fmt.Errorf("unknown assertion type: %s", assertion.Type)
	}
}

// evaluateLLMJudge calls the LLM referenced by judgeModelConfigRef to evaluate
// whether the artifact meets the criteria. The LLM acts as a binary classifier
// returning pass/fail.
func (r *Runner) evaluateLLMJudge(ctx context.Context, assertion karov1alpha1.Assertion, artifact string) (bool, error) {
	if assertion.JudgeModelConfigRef == nil {
		return false, fmt.Errorf("llm-judge assertion requires judgeModelConfigRef")
	}

	// Fetch the ModelConfig.
	var modelConfig karov1alpha1.ModelConfig
	key := types.NamespacedName{
		Name:      assertion.JudgeModelConfigRef.Name,
		Namespace: r.namespace,
	}
	if err := r.client.Get(ctx, key, &modelConfig); err != nil {
		return false, fmt.Errorf("failed to fetch judge ModelConfig %q: %w", assertion.JudgeModelConfigRef.Name, err)
	}

	// Fetch the API key from the referenced secret.
	apiKey, err := r.resolveAPIKey(ctx, &modelConfig)
	if err != nil {
		return false, fmt.Errorf("failed to resolve API key for judge: %w", err)
	}

	// Call the LLM judge.
	return r.llmCaller.Judge(ctx, modelConfig.Spec.Provider, modelConfig.Spec.Name, apiKey, assertion.Criteria, artifact)
}

// resolveAPIKey fetches the API key from the secret referenced by the ModelConfig.
func (r *Runner) resolveAPIKey(ctx context.Context, mc *karov1alpha1.ModelConfig) (string, error) {
	if mc.Spec.APIKeySecret == nil {
		return "", fmt.Errorf("ModelConfig %s has no apiKeySecret", mc.Name)
	}

	var secret corev1.Secret
	key := types.NamespacedName{
		Name:      mc.Spec.APIKeySecret.Name,
		Namespace: r.namespace,
	}
	if err := r.client.Get(ctx, key, &secret); err != nil {
		return "", fmt.Errorf("secret %q not found: %w", mc.Spec.APIKeySecret.Name, err)
	}

	secretKey := mc.Spec.APIKeySecret.Key
	if secretKey == "" {
		secretKey = "ANTHROPIC_API_KEY" // sensible default
	}

	data, ok := secret.Data[secretKey]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %q", secretKey, mc.Spec.APIKeySecret.Name)
	}

	return string(data), nil
}
