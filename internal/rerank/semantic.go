package rerank

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/amir/web-fetch-server/internal/search"
)

type semantic struct {
	logger  *slog.Logger
	scoreFn ScoreFn
}

// NewSemantic returns a reranker that adds an external cross-encoder rerank
// vote (scoreFn) on top of the BM25 and engine-consensus signals. Any scoreFn
// failure is fail-open: the order degrades to the plain rrf result.
func NewSemantic(logger *slog.Logger, scoreFn ScoreFn) Reranker {
	if logger == nil {
		logger = slog.Default()
	}
	return &semantic{logger: logger, scoreFn: scoreFn}
}

func (s *semantic) Rank(query string, in []search.Result) (out []search.Result) {
	if len(in) < 2 || strings.TrimSpace(query) == "" {
		return in
	}
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Warn("rerank: panic recovered, returning original order", "panic", rec)
			out = in
		}
	}()

	voteLists := [][]vote{bm25Votes(query, in), engineVotes(in)}

	docs := make([]string, len(in))
	for i, res := range in {
		docs[i] = res.Title + " " + res.Snippet
	}
	scores, err := s.scoreFn(context.Background(), query, docs)
	if err != nil {
		s.logger.Warn("rerank: semantic scoring failed, using rrf signals only", "error", err.Error())
	} else {
		order := orderedIdx(len(in))
		sort.SliceStable(order, func(x, y int) bool { return scores[order[x]] > scores[order[y]] })
		voteLists = append(voteLists, positionalVotes(order))
	}

	return finalize(in, rrfFuse(len(in), voteLists...))
}
