package rerank

import (
	"reflect"
	"sort"
	"testing"

	"github.com/amir/web-fetch-server/internal/search"
)

func urls(in []search.Result) []string {
	out := make([]string, len(in))
	for i, r := range in {
		out[i] = r.URL
	}
	return out
}

func enginesN(name string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = name
	}
	return out
}

func TestRankPromotesRelevantFromMiddle(t *testing.T) {
	query := "настройка nginx reverse proxy"
	in := []search.Result{
		{Title: "Настройка роутера", URL: "https://router.example", Snippet: "Простая настройка домашнего роутера и wi-fi сети"},
		{Title: "Новости спорта сегодня", URL: "https://sport.example", Snippet: "Обзор матча и интервью с тренером после игры"},
		{Title: "Рецепт домашнего борща", URL: "https://food.example", Snippet: "Пошаговый рецепт борща со свёклой и капустой"},
		{Title: "Погода на неделю", URL: "https://weather.example", Snippet: "Прогноз погоды: осадки, температура и ветер в городах"},
		{Title: "Курс валют на сегодня", URL: "https://fx.example", Snippet: "Актуальный курс доллара и евро к рублю"},
		{Title: "nginx reverse proxy: настройка", URL: "https://nginx.example", Snippet: "Полное руководство по настройке nginx как reverse proxy: proxy_pass, gzip и кэширование"},
		{Title: "Отзывы о фильме", URL: "https://movies.example", Snippet: "Премьера недели: отзывы зрителей и рейтинг"},
		{Title: "Расписание электричек", URL: "https://trains.example", Snippet: "Актуальное расписание пригородных поездов на сегодня"},
	}
	got := NewRRF(nil).Rank(query, in)

	pos := -1
	for i, r := range got {
		if r.URL == "https://nginx.example" {
			pos = i
		}
	}
	if pos < 0 {
		t.Fatal("relevant result lost after rerank")
	}
	if pos > 2 {
		t.Fatalf("relevant result at position %d, want top-3", pos+1)
	}
}

func TestRankEngineConsensus(t *testing.T) {
	mk := func(enginesFirst, enginesSecond int) []search.Result {
		return []search.Result{
			{Title: "Первый", URL: "https://first.example", Snippet: "Совершенно другой текст про природу", Engines: enginesN("google", enginesFirst)},
			{Title: "Второй", URL: "https://second.example", Snippet: "Ещё один посторонний текст", Engines: enginesN("google", enginesSecond)},
		}
	}

	got := NewRRF(nil).Rank("запрос", mk(1, 3))
	if got[0].URL != "https://second.example" {
		t.Fatalf("consensus should lift the 3-engine result, got %s first", got[0].URL)
	}

	got = NewRRF(nil).Rank("запрос", mk(3, 1))
	if got[0].URL != "https://first.example" {
		t.Fatalf("consensus should keep the 3-engine result on top, got %s first", got[0].URL)
	}
}

func TestRankTextMatchBeatsEngines(t *testing.T) {
	in := []search.Result{
		{Title: "Много движков", URL: "https://many.example", Snippet: "Посторонний текст без совпадений", Engines: enginesN("google", 5)},
		{Title: "nginx настройка", URL: "https://nginx.example", Snippet: "Настройка nginx под нагрузкой", Engines: enginesN("google", 1)},
	}
	got := NewRRF(nil).Rank("nginx", in)
	if got[0].URL != "https://nginx.example" {
		t.Fatalf("text match should beat engine count, got %s first", got[0].URL)
	}
}

func TestRankKeepsAllElements(t *testing.T) {
	in := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: "текст один", Engines: enginesN("google", 1)},
		{Title: "b", URL: "https://b.example", Snippet: "текст два", Engines: enginesN("google", 2)},
		{Title: "c", URL: "https://c.example", Snippet: "текст три"},
		{Title: "d", URL: "https://d.example", Snippet: "nginx совсем другой текст"},
		{Title: "e", URL: "https://e.example", Snippet: "текст пять", Engines: enginesN("bing", 1)},
		{Title: "f", URL: "https://f.example", Snippet: "текст шесть", Engines: enginesN("google", 3)},
	}
	got := NewRRF(nil).Rank("nginx", in)

	if len(got) != len(in) {
		t.Fatalf("got %d results, want %d", len(got), len(in))
	}
	want := urls(in)
	gotURLs := urls(got)
	sort.Strings(want)
	sort.Strings(gotURLs)
	if !reflect.DeepEqual(want, gotURLs) {
		t.Fatalf("URLs changed after rerank: got %v, want %v", gotURLs, want)
	}
	seen := map[string]bool{}
	for _, u := range gotURLs {
		if seen[u] {
			t.Fatalf("duplicate result after rerank: %s", u)
		}
		seen[u] = true
	}
}

func TestRankSetsScoreAndFlag(t *testing.T) {
	in := []search.Result{
		{Title: "один", URL: "https://a.example", Snippet: "первый текст"},
		{Title: "nginx настройка", URL: "https://b.example", Snippet: "настройка nginx", Engines: enginesN("google", 2)},
	}
	got := NewRRF(nil).Rank("nginx", in)
	for i, r := range got {
		if !r.Reranked {
			t.Errorf("result %d: reranked = false, want true", i)
		}
		if r.Score <= 0 {
			t.Errorf("result %d: score = %v, want > 0", i, r.Score)
		}
	}
	if got[0].URL != "https://b.example" {
		t.Fatalf("relevant result should rank first, got %s", got[0].URL)
	}
}

func TestRankDeterministic(t *testing.T) {
	in := []search.Result{
		{Title: "alpha beta", URL: "https://a.example", Snippet: "beta gamma", Engines: enginesN("google", 1)},
		{Title: "beta", URL: "https://b.example", Snippet: "alpha", Engines: enginesN("google", 2)},
		{Title: "gamma delta", URL: "https://c.example", Snippet: "delta epsilon", Engines: enginesN("google", 3)},
		{Title: "delta", URL: "https://d.example", Snippet: "alpha beta gamma delta epsilon"},
	}
	first := urls(NewRRF(nil).Rank("beta alpha", in))
	for i := 0; i < 5; i++ {
		again := urls(NewRRF(nil).Rank("beta alpha", in))
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("rerank is not deterministic: run %d = %v, first = %v", i, again, first)
		}
	}
}

func TestNonePassthrough(t *testing.T) {
	in := []search.Result{
		{Title: "второй", URL: "https://b.example", Snippet: "не про запрос", Engines: enginesN("google", 3)},
		{Title: "первый nginx", URL: "https://a.example", Snippet: "nginx в самом низу"},
	}
	got := NewNone().Rank("nginx", in)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("passthrough changed results: got %+v, want %+v", got, in)
	}
	for i, r := range got {
		if r.Reranked {
			t.Errorf("result %d: reranked = true, want false", i)
		}
		if r.Score != 0 {
			t.Errorf("result %d: score = %v, want 0", i, r.Score)
		}
	}
}

func TestRankDegenerateInputs(t *testing.T) {
	rk := NewRRF(nil)

	if got := rk.Rank("", nil); got != nil {
		t.Errorf("empty query + nil list: got %v, want nil", got)
	}
	if got := rk.Rank("   ", nil); got != nil {
		t.Errorf("blank query + nil list: got %v, want nil", got)
	}

	in := []search.Result{{Title: "x", URL: "https://x.example", Snippet: "y"}}
	if got := rk.Rank("запрос", in); !reflect.DeepEqual(got, in) {
		t.Errorf("single result should pass through, got %+v", got)
	}

	three := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: "текст"},
		{Title: "b", URL: "https://b.example", Snippet: "текст"},
		{Title: "c", URL: "https://c.example", Snippet: "текст"},
	}
	if got := rk.Rank("", three); !reflect.DeepEqual(got, three) {
		t.Errorf("empty query should pass through, got %+v", got)
	}
}

func TestRankUnknownQueryTermsPreserveOrder(t *testing.T) {
	in := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: "текст один"},
		{Title: "b", URL: "https://b.example", Snippet: "текст два"},
		{Title: "c", URL: "https://c.example", Snippet: "текст три"},
	}
	got := urls(NewRRF(nil).Rank("абракадабра", in))
	if !reflect.DeepEqual(got, urls(in)) {
		t.Fatalf("query with no corpus matches should preserve original order, got %v", got)
	}
}
