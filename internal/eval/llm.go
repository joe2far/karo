package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPLLMCaller implements LLMCaller by calling provider APIs over HTTP.
type HTTPLLMCaller struct {
	HTTPClient *http.Client
}

func (c *HTTPLLMCaller) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// Judge calls the LLM to evaluate whether the artifact meets the criteria.
// Returns true if the LLM judges the criteria as met.
func (c *HTTPLLMCaller) Judge(ctx context.Context, provider, model, apiKey, criteria, artifact string) (bool, error) {
	prompt := buildJudgePrompt(criteria, artifact)

	switch provider {
	case "anthropic":
		return c.callAnthropic(ctx, model, apiKey, prompt)
	case "openai":
		return c.callOpenAI(ctx, model, apiKey, prompt)
	default:
		// For other providers (bedrock, vertex), fall back to Anthropic-style if
		// an API key is provided, otherwise return an error.
		if apiKey != "" {
			return c.callAnthropic(ctx, model, apiKey, prompt)
		}
		return false, fmt.Errorf("unsupported LLM judge provider: %s", provider)
	}
}

// buildJudgePrompt creates the system+user prompt for the judge LLM.
// The prompt is designed to elicit a clear PASS/FAIL response.
func buildJudgePrompt(criteria, artifact string) string {
	return fmt.Sprintf(`You are an evaluation judge. Your job is to determine whether the submitted output meets the given criteria.

Criteria: %s

Submitted Output:
---
%s
---

Evaluate whether the submitted output meets the criteria. Respond with exactly one word on the first line: PASS or FAIL. Then optionally explain your reasoning on subsequent lines.`, criteria, artifact)
}

// callAnthropic calls the Anthropic Messages API.
func (c *HTTPLLMCaller) callAnthropic(ctx context.Context, model, apiKey, prompt string) (bool, error) {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 256,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return false, fmt.Errorf("anthropic API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("anthropic API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode anthropic response: %w", err)
	}

	if len(result.Content) == 0 {
		return false, fmt.Errorf("empty response from anthropic")
	}

	return parseJudgeResponse(result.Content[0].Text), nil
}

// callOpenAI calls the OpenAI Chat Completions API.
func (c *HTTPLLMCaller) callOpenAI(ctx context.Context, model, apiKey, prompt string) (bool, error) {
	if model == "" {
		model = "gpt-4o"
	}

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": 256,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return false, fmt.Errorf("openai API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("openai API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode openai response: %w", err)
	}

	if len(result.Choices) == 0 {
		return false, fmt.Errorf("empty response from openai")
	}

	return parseJudgeResponse(result.Choices[0].Message.Content), nil
}

// parseJudgeResponse extracts PASS or FAIL from the LLM response.
// Looks at the first line/word of the response.
func parseJudgeResponse(response string) bool {
	response = strings.TrimSpace(response)
	if response == "" {
		return false
	}

	// Take the first line.
	firstLine := response
	if idx := strings.IndexByte(response, '\n'); idx != -1 {
		firstLine = response[:idx]
	}
	firstLine = strings.TrimSpace(firstLine)
	upper := strings.ToUpper(firstLine)

	// Check for PASS/FAIL at the start.
	if strings.HasPrefix(upper, "PASS") {
		return true
	}
	if strings.HasPrefix(upper, "FAIL") {
		return false
	}

	// Fallback: check if the response contains pass/fail anywhere on the first line.
	if strings.Contains(upper, "PASS") && !strings.Contains(upper, "FAIL") {
		return true
	}

	return false
}
