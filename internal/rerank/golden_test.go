package rerank

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amir/web-fetch-server/internal/search"
)

type goldenCase struct {
	Query    string   `json:"query"`
	Expected []string `json:"expected"`
}

func loadGolden(t *testing.T) []goldenCase {
	t.Helper()
	b, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatalf("read golden set: %v", err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatalf("parse golden set: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("golden set is empty")
	}
	return cases
}

// TestGoldenSetLive measures top-3 hit rate and MRR of each rerank mode
// against the golden set using a live SearXNG and (optionally) the rerank
// API. It never runs in CI: opt in with GOLDEN_LIVE=1.
func TestGoldenSetLive(t *testing.T) {
	if os.Getenv("GOLDEN_LIVE") != "1" {
		t.Skip("live measurement: set GOLDEN_LIVE=1 (plus SEARXNG_URL, optionally RERANK_API_KEY) to enable")
	}
	cases := loadGolden(t)

	client := search.NewClientWithLogger(envOr("SEARXNG_URL", "http://localhost:8888"), "", 20*time.Second, nil)

	rerankers := map[string]Reranker{
		"none": NewNone(),
		"rrf":  NewRRF(nil),
	}
	if key := os.Getenv("RERANK_API_KEY"); key != "" {
		fn := NewRouterAIScoreFn(envOr("RERANK_API_URL", defaultRerankURL), key, envOr("RERANK_MODEL", defaultRerankModel), 10*time.Second)
		rerankers["semantic"] = NewSemantic(nil, fn)
	} else {
		t.Log("RERANK_API_KEY not set — semantic mode skipped")
	}

	type summary struct {
		run  int
		hits int
		mrr  float64
	}
	sum := map[string]*summary{}
	for name := range rerankers {
		sum[name] = &summary{}
	}

	for _, c := range cases {
		resp, err := client.Search(context.Background(), c.Query, "", "", 10)
		if err != nil {
			t.Errorf("search %q: %v", c.Query, err)
			continue
		}
		res := resp.Results
		for name, rk := range rerankers {
			got := rk.Rank(c.Query, res)
			s := sum[name]
			s.run++
			rank := firstExpectedRank(c.Expected, got)
			if rank == 0 {
				t.Logf("%-55q %-8s miss", c.Query, name)
				continue
			}
			s.mrr += 1 / float64(rank)
			if rank <= 3 {
				s.hits++
			}
			t.Logf("%-55q %-8s rank=%d url=%s", c.Query, name, rank, got[rank-1].URL)
		}
	}

	for _, name := range []string{"none", "rrf", "semantic"} {
		s, ok := sum[name]
		if !ok || s.run == 0 {
			continue
		}
		t.Logf("SUMMARY %-9s top3=%d/%d (%.0f%%)  mrr=%.3f", name, s.hits, s.run, 100*float64(s.hits)/float64(s.run), s.mrr/float64(s.run))
	}
}

func firstExpectedRank(expected []string, got []search.Result) int {
	for i, r := range got {
		host := hostOf(r.URL)
		for _, e := range expected {
			if strings.Contains(host, strings.ToLower(e)) {
				return i + 1
			}
		}
	}
	return 0
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.ToLower(raw)
	}
	return strings.ToLower(u.Host)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
