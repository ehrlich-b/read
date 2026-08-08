package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	clientTimeout  = 120 * time.Second
)

// Client is an OpenAI-compatible chat-completions client.
type Client struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// New constructs a Client for any OpenAI-compatible endpoint. An empty
// baseURL defaults to the OpenAI API.
func New(baseURL, model, apiKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: clientTimeout},
	}
}

// ChatSingle sends a single user message to {baseURL}/chat/completions and
// returns the text of choices[0].message.content.
func (c *Client) ChatSingle(message string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:    c.model,
		Messages: []chatMessage{{Role: "user", Content: message}},
	})
	if err != nil {
		return "", fmt.Errorf("llm: marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: chat returned %d: %s", resp.StatusCode, respBody)
	}

	var result chatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("llm: unmarshal response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("llm: no choices in response")
	}
	return result.Choices[0].Message.Content, nil
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}
