package rerank

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

const (
	defaultRerankModel = "voyageai/rerank-2.5-lite"
	defaultRerankURL   = "https://routerai.ru/api/v1"
	maxDocRunes        = 1000
)

// ScoreFn scores documents against a query: N documents in, N relevance
// scores out (same order), or an error. Implementations must be safe for
// sequential use.
type ScoreFn func(ctx context.Context, query string, docs []string) ([]float64, error)

// NewRouterAIScoreFn returns a ScoreFn backed by a Cohere-compatible rerank
// endpoint (default: RouterAI, https://routerai.ru/api/v1/rerank).
func NewRouterAIScoreFn(baseURL, apiKey, model string, timeout time.Duration) ScoreFn {
	if baseURL == "" {
		baseURL = defaultRerankURL
	}
	if model == "" {
		model = defaultRerankModel
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	c := &routerai{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: timeout},
	}
	return c.Score
}

type routerai struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func (c *routerai) Score(ctx context.Context, query string, docs []string) ([]float64, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	payload := struct {
		Model     string   `json:"model"`
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
	}{c.model, query, truncateAll(docs, maxDocRunes)}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal rerank request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("rerank api status %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Score          float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}
	if len(parsed.Results) == 0 {
		return nil, fmt.Errorf("rerank response has no results")
	}

	out := make([]float64, len(docs))
	for _, r := range parsed.Results {
		if r.Index < 0 || r.Index >= len(docs) {
			continue
		}
		if r.RelevanceScore != 0 {
			out[r.Index] = r.RelevanceScore
		} else {
			out[r.Index] = r.Score
		}
	}
	return out, nil
}

func truncateAll(docs []string, n int) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		r := []rune(d)
		if len(r) > n {
			out[i] = string(r[:n])
		} else {
			out[i] = d
		}
	}
	return out
}
