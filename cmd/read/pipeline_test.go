package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ehrlich-b/read/internal/llm"
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
