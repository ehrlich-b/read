package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ehrlich-b/read/internal/embedding"
	"github.com/ehrlich-b/read/internal/llm"
	"github.com/ehrlich-b/read/internal/relay"
)

const testModel = "test-model"
const testKey = "test-key"

func chatServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// openaiBackend returns a backend pointed at the fake server's base URL.
func openaiBackend(t *testing.T, ts *httptest.Server) *backend {
	t.Helper()
	return &backend{kind: "openai", client: llm.New(ts.URL, testModel, testKey)}
}

type chatRequestBody struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func decodeReqBody(t *testing.T, body []byte) chatRequestBody {
	t.Helper()
	var req chatRequestBody
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return req
}

func TestOpenAICompressRoundTrip(t *testing.T) {
	title, source, text := "Some Title", "Test Source", "article body 123"
	var requests int
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q, want %q", got, "Bearer "+testKey)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		req := decodeReqBody(t, body)
		if req.Model != testModel {
			t.Errorf("model = %q, want %q", req.Model, testModel)
		}
		if len(req.Messages) != 1 {
			t.Fatalf("messages = %d, want 1", len(req.Messages))
		}
		if got := req.Messages[0].Role; got != "user" {
			t.Errorf("role = %q, want user", got)
		}
		want := compressPrompt(title, source) + "\n\n" + text
		if got := req.Messages[0].Content; got != want {
			t.Errorf("message content does not match compress prompt + article text")
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"[Test Source] A dense 800-char summary."}}]}`)
	})

	b := openaiBackend(t, ts)
	comp, err := b.compress(title, source, text)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if comp != "[Test Source] A dense 800-char summary." {
		t.Errorf("comp = %q", comp)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
	if isBotRefusal(comp) {
		t.Error("expected normal completion to pass refusal check")
	}
}

func TestOpenAIScoreParsesWordyReply(t *testing.T) {
	title, source, text := "Some Title", "Test Source", "article body 123"
	scorerPrompt := "You are a strict scorer. Output SCORE N only."
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		req := decodeReqBody(t, body)
		want := scorerPrompt + "\n\n" + scoreInput(title, source, text)
		if got := req.Messages[0].Content; got != want {
			t.Errorf("message content does not match scorer prompt + article")
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"This piece has genuine depth and benchmarks.\nSCORE 180\nIt would do well."}}]}`)
	})

	b := openaiBackend(t, ts)
	mass, err := b.score(title, source, text, scorerPrompt)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if mass != 180 {
		t.Errorf("mass = %d, want 180 (number parsed out of wordy reply)", mass)
	}
}

func TestOpenAISCOREWithoutNumberDefaults(t *testing.T) {
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"This is unstoppable nonsense without a score."}}]}`)
	})

	b := openaiBackend(t, ts)
	mass, err := b.score("Some Title", "Test Source", "article body 123", "scorer")
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if mass != 10 {
		t.Errorf("mass = %d, want 10 (default when no SCORE line)", mass)
	}
}

func TestOpenAIHTTP500PropagatesAsError(t *testing.T) {
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	})

	b := openaiBackend(t, ts)
	if _, err := b.compress("Some Title", "Test Source", "article body 123"); err == nil {
		t.Error("expected compress to return an error on HTTP 500")
	}
	// Score matches the claude path's tolerance: upstream failure -> default 10, not an error.
	mass, err := b.score("Some Title", "Test Source", "article body 123", "scorer")
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if mass != 10 {
		t.Errorf("mass = %d, want 10 (default on failure)", mass)
	}
}

func TestOpenAIRefusalRejectedByRegex(t *testing.T) {
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"I don't have access to the article content, but I can tell you that subscribe to continue is required."}}]}`)
	})

	b := openaiBackend(t, ts)
	comp, err := b.compress("Some Title", "Test Source", "article body 123")
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !isBotRefusal(comp) {
		t.Errorf("expected refusal completion to be rejected by refusal regex: %q", comp)
	}
}

// chatCompletion wraps assistant content in the OpenAI chat-completions envelope
// with proper JSON escaping.
func chatCompletion(t *testing.T, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"choices": []map[string]any{{
			"message": map[string]any{"role": "assistant", "content": content},
		}},
	})
	if err != nil {
		t.Fatalf("marshal chat completion: %v", err)
	}
	return string(b)
}

// stubEmbed satisfies embedding.Embedder for post creation in batched-flow tests.
type stubEmbed struct {
	name string
	vec  []float32
}

func (s *stubEmbed) Embed(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, len(s.vec))
		copy(v, s.vec)
		out[i] = v
	}
	return out, nil
}
func (s *stubEmbed) Dims() int    { return len(s.vec) }
func (s *stubEmbed) Name() string { return s.name }

const batchScorer = "You are a strict scorer. Output SCORE N only."

// batchTestStore seeds an in-memory store with the given articles and returns
// their pending rows (with ids assigned) plus a stub embedder and a discard
// logger for driving processArticles.
func batchTestStore(t *testing.T, seeds ...relay.PipelineArticle) (*relay.RelayStore, []relay.PipelineArticle, embedding.Embedder, *log.Logger) {
	t.Helper()
	store, err := relay.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	for _, a := range seeds {
		if err := store.InsertPipelineArticle(a.Link, a.Title, a.Source, a.RawText, a.PublishedAt); err != nil {
			t.Fatalf("insert pipeline article: %v", err)
		}
	}
	pending, err := store.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != len(seeds) {
		t.Fatalf("pending = %d, want %d", len(pending), len(seeds))
	}

	return store, pending, &stubEmbed{name: "stub", vec: []float32{0.1, 0.2, 0.3, 0.4}}, log.New(io.Discard, "", 0)
}

func assertArticle(t *testing.T, store *relay.RelayStore, id int, wantStatus, wantComp string, wantMass int) {
	t.Helper()
	var status, skipReason, comp string
	var mass int
	err := store.DB().QueryRow(
		`SELECT status, COALESCE(skip_reason,''), COALESCE(compressed_text,''), COALESCE(mass,0) FROM pipeline_articles WHERE id = ?`, id,
	).Scan(&status, &skipReason, &comp, &mass)
	if err != nil {
		t.Fatalf("query article %d: %v", id, err)
	}
	if status != wantStatus {
		t.Errorf("article %d status = %q, want %q", id, status, wantStatus)
	}
	if wantComp != "" && comp != wantComp {
		t.Errorf("article %d compressed = %q, want %q", id, comp, wantComp)
	}
	if mass != wantMass {
		t.Errorf("article %d mass = %d, want %d", id, mass, wantMass)
	}
}

func TestBatchThreeArticlesOneRequest(t *testing.T) {
	seed := []relay.PipelineArticle{
		{Link: "http://t/1", Title: "One", Source: "S1", RawText: "body one 12345"},
		{Link: "http://t/2", Title: "Two", Source: "S2", RawText: "body two 12345"},
		{Link: "http://t/3", Title: "Three", Source: "S3", RawText: "body three 12345"},
	}

	var requests int
	var sawSystem, sawUser bool
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		req := decodeReqBody(t, body)
		if len(req.Messages) != 2 {
			t.Fatalf("batch messages = %d, want 2 (system + user)", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("message[0].role = %q, want system", req.Messages[0].Role)
		}
		if req.Messages[1].Role != "user" {
			t.Errorf("message[1].role = %q, want user", req.Messages[1].Role)
		}
		if !strings.Contains(req.Messages[0].Content, compressInstructions) {
			t.Error("system prompt must embed the compress instructions")
		}
		if !strings.Contains(req.Messages[0].Content, batchScorer) {
			t.Error("system prompt must embed the scorer rules")
		}
		for _, id := range []string{"1", "2", "3"} {
			if !strings.Contains(req.Messages[1].Content, "=== ARTICLE "+id+" ===") {
				t.Errorf("user content missing marker for article %s", id)
			}
		}
		if !strings.Contains(req.Messages[1].Content, "=====NEXT ARTICLE=====") {
			t.Error("user content missing article delimiter")
		}
		sawSystem, sawUser = true, true
		// All three present but in a shuffled order, wrapped in a markdown fence.
		content := "```json\n" + `[{"id":3,"summary":"summary three","score":700},{"id":1,"summary":"summary one","score":120},{"id":2,"summary":"summary two","score":250}]` + "\n```"
		fmt.Fprint(w, chatCompletion(t, content))
	})

	store, pending, emb, logger := batchTestStore(t, seed...)
	_, posted, skipped := processArticles(store, emb, openaiBackend(t, ts), logger, pending, batchScorer, 3)

	if !sawSystem || !sawUser {
		t.Fatal("batch request body was never decoded")
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (all three articles in one POST)", requests)
	}
	if posted != 3 || skipped != 0 {
		t.Errorf("posted = %d, skipped = %d, want posted=3 skipped=0", posted, skipped)
	}
	// Pairing must be by id, not by array position.
	assertArticle(t, store, pending[0].ID, "posted", "summary one", 120)
	assertArticle(t, store, pending[1].ID, "posted", "summary two", 250)
	assertArticle(t, store, pending[2].ID, "posted", "summary three", 700)
}

func TestBatchMissingIDLeavesPending(t *testing.T) {
	seed := []relay.PipelineArticle{
		{Link: "http://t/11", Title: "Eleven", Source: "S1", RawText: "body eleven 12345"},
		{Link: "http://t/12", Title: "Twelve", Source: "S2", RawText: "body twelve 12345"},
		{Link: "http://t/13", Title: "Thirteen", Source: "S3", RawText: "body thirteen 12345"},
	}
	store, pending, emb, logger := batchTestStore(t, seed...)

	var requests int
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		// Omit the first article's id; include both of the others plus unknown ids
		// that must be ignored.
		resp := fmt.Sprintf(
			`[{"id":99999,"summary":"unknown ignored","score":1},{"id":%d,"summary":"summary twelve","score":250},{"id":%d,"summary":"summary thirteen","score":700}]`,
			pending[1].ID, pending[2].ID)
		fmt.Fprint(w, chatCompletion(t, resp))
	})

	_, posted, skipped := processArticles(store, emb, openaiBackend(t, ts), logger, pending, batchScorer, 3)

	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
	if posted != 2 || skipped != 0 {
		t.Errorf("posted = %d, skipped = %d, want posted=2 skipped=0", posted, skipped)
	}

	// The missing id stays pending (its status is untouched)...
	remaining, err := store.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != pending[0].ID {
		t.Errorf("remaining pending = %v, want exactly article %d", remaining, pending[0].ID)
	}
	// ...while the other two complete with their own summaries/scores.
	assertArticle(t, store, pending[1].ID, "posted", "summary twelve", 250)
	assertArticle(t, store, pending[2].ID, "posted", "summary thirteen", 700)
}

func TestBatchDuplicateIDUsesFirst(t *testing.T) {
	seed := []relay.PipelineArticle{
		{Link: "http://t/41", Title: "One", Source: "S1", RawText: "body 41 12345"},
		{Link: "http://t/42", Title: "Two", Source: "S2", RawText: "body 42 12345"},
	}
	store, pending, emb, logger := batchTestStore(t, seed...)

	var requests int
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		// The same id appears twice: the first occurrence must win, the extra ignored.
		resp := fmt.Sprintf(
			`[{"id":%d,"summary":"first summary","score":300},{"id":%d,"summary":"duplicate summary","score":999},{"id":%d,"summary":"second summary","score":55}]`,
			pending[0].ID, pending[0].ID, pending[1].ID)
		fmt.Fprint(w, chatCompletion(t, resp))
	})

	_, posted, skipped := processArticles(store, emb, openaiBackend(t, ts), logger, pending, batchScorer, 3)

	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
	if posted != 2 || skipped != 0 {
		t.Errorf("posted = %d, skipped = %d, want posted=2 skipped=0", posted, skipped)
	}
	assertArticle(t, store, pending[0].ID, "posted", "first summary", 300)
	assertArticle(t, store, pending[1].ID, "posted", "second summary", 55)
}

func TestBatchMalformedJSONRetriesThenAllPending(t *testing.T) {
	seed := []relay.PipelineArticle{
		{Link: "http://t/21", Title: "Twenty one", Source: "S1", RawText: "body 21 12345"},
		{Link: "http://t/22", Title: "Twenty two", Source: "S2", RawText: "body 22 12345"},
		{Link: "http://t/23", Title: "Twenty three", Source: "S3", RawText: "body 23 12345"},
	}

	var requests int
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		// Fenced, but still not valid JSON after the fences are stripped.
		fmt.Fprint(w, chatCompletion(t, "```json\nthis is absolutely not json\n```"))
	})

	store, pending, emb, logger := batchTestStore(t, seed...)
	_, posted, skipped := processArticles(store, emb, openaiBackend(t, ts), logger, pending, batchScorer, 3)

	if requests != 2 {
		t.Errorf("requests = %d, want 2 (initial request + one whole-batch retry)", requests)
	}
	if posted != 0 || skipped != 0 {
		t.Errorf("posted = %d, skipped = %d, want both 0", posted, skipped)
	}
	remaining, err := store.ListPendingArticles(0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(remaining) != 3 {
		t.Errorf("remaining pending = %d, want 3 (all left pending)", len(remaining))
	}
}

func TestBatchOneUsesSingleArticlePath(t *testing.T) {
	seed := []relay.PipelineArticle{
		{Link: "http://t/31", Title: "Alpha", Source: "S1", RawText: "body alpha 12345"},
		{Link: "http://t/32", Title: "Beta", Source: "S2", RawText: "body beta 12345"},
	}

	var requests int
	ts := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		req := decodeReqBody(t, body)
		if len(req.Messages) != 1 {
			t.Fatalf("single-path messages = %d, want 1 (no system prompt)", len(req.Messages))
		}
		content := req.Messages[0].Content
		if strings.HasPrefix(content, compressInstructions) {
			// The existing compress request: prompt + article text.
			if !strings.Contains(content, seed[0].RawText) && !strings.Contains(content, seed[1].RawText) {
				t.Errorf("compress request missing article text")
			}
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"[Test Source] single summary"}}]}`)
			return
		}
		// The existing score request: scorer prompt + article input.
		if !strings.HasPrefix(content, batchScorer) {
			t.Errorf("score request should start with the scorer prompt, got %q", content[:min(len(content), 40)])
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"SCORE 180"}}]}`)
	})

	store, pending, emb, logger := batchTestStore(t, seed...)
	_, posted, skipped := processArticles(store, emb, openaiBackend(t, ts), logger, pending, batchScorer, 1)

	// One compress POST + one score POST per article, batch never engaged.
	if requests != 4 {
		t.Errorf("requests = %d, want 4 (2 articles x compress+score)", requests)
	}
	if posted != 2 || skipped != 0 {
		t.Errorf("posted = %d, skipped = %d, want posted=2 skipped=0", posted, skipped)
	}
	assertArticle(t, store, pending[0].ID, "posted", "[Test Source] single summary", 180)
	assertArticle(t, store, pending[1].ID, "posted", "[Test Source] single summary", 180)
}
