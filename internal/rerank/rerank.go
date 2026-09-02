package rerank

import (
	"log/slog"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/amir/web-fetch-server/internal/search"
)

type Reranker interface {
	Rank(query string, in []search.Result) []search.Result
}

const (
	rrfK   = 60
	bm25K1 = 1.5
	bm25B  = 0.75
)

type rrf struct {
	logger *slog.Logger
}

func NewRRF(logger *slog.Logger) Reranker {
	if logger == nil {
		logger = slog.Default()
	}
	return &rrf{logger: logger}
}

type passthrough struct{}

func NewNone() Reranker { return passthrough{} }

func (passthrough) Rank(_ string, in []search.Result) []search.Result { return in }

func (r *rrf) Rank(query string, in []search.Result) (out []search.Result) {
	if len(in) < 2 || strings.TrimSpace(query) == "" {
		return in
	}
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Warn("rerank: panic recovered, returning original order", "panic", rec)
			out = in
		}
	}()

	votes := [][]vote{bm25Votes(query, in), engineVotes(in)}
	return finalize(in, rrfFuse(len(in), votes...))
}

type vote struct {
	idx  int
	rank int
}

type fused struct {
	order  []int
	scores []float64
}

func rrfFuse(n int, voteLists ...[]vote) fused {
	scores := make([]float64, n)
	for _, list := range voteLists {
		for _, v := range list {
			scores[v.idx] += 1 / (rrfK + float64(v.rank))
		}
	}
	order := orderedIdx(n)
	sort.SliceStable(order, func(x, y int) bool { return scores[order[x]] > scores[order[y]] })
	return fused{order: order, scores: scores}
}

func finalize(in []search.Result, f fused) []search.Result {
	out := make([]search.Result, len(in))
	for i, idx := range f.order {
		out[i] = in[idx]
		out[i].Score = f.scores[idx]
		out[i].Reranked = true
	}
	return out
}

func positionalVotes(order []int) []vote {
	votes := make([]vote, len(order))
	for i, idx := range order {
		votes[i] = vote{idx: idx, rank: i + 1}
	}
	return votes
}

func bm25Votes(query string, in []search.Result) []vote {
	n := len(in)
	tfs := make([]map[string]int, n)
	dls := make([]int, n)
	df := make(map[string]int)
	totalLen := 0
	for i, res := range in {
		toks := tokenize(res.Title + " " + res.Snippet)
		m := make(map[string]int, len(toks))
		for _, t := range toks {
			m[t]++
		}
		tfs[i] = m
		dls[i] = len(toks)
		for t := range m {
			df[t]++
		}
		totalLen += len(toks)
	}
	avgdl := float64(totalLen) / float64(n)

	bm25 := make([]float64, n)
	for _, qt := range tokenize(query) {
		d, ok := df[qt]
		if !ok {
			continue
		}
		idf := math.Log(1 + (float64(n)-float64(d)+0.5)/(float64(d)+0.5))
		for i := range in {
			tf := float64(tfs[i][qt])
			if tf == 0 {
				continue
			}
			bm25[i] += idf * tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*float64(dls[i])/avgdl))
		}
	}

	order := orderedIdx(n)
	sort.SliceStable(order, func(x, y int) bool { return bm25[order[x]] > bm25[order[y]] })
	for pos, idx := range order {
		if bm25[idx] <= 0 {
			return positionalVotes(order[:pos])
		}
	}
	return positionalVotes(order)
}

func engineVotes(in []search.Result) []vote {
	order := orderedIdx(len(in))
	sort.SliceStable(order, func(x, y int) bool {
		return len(in[order[x]].Engines) > len(in[order[y]].Engines)
	})
	votes := make([]vote, len(order))
	prevCount := -1
	rank := 1
	for pos, idx := range order {
		cnt := len(in[idx].Engines)
		if cnt != prevCount {
			rank = pos + 1
			prevCount = cnt
		}
		votes[pos] = vote{idx: idx, rank: rank}
	}
	return votes
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func orderedIdx(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}
