package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"picoclaw/pkg/providers"
)

const (
	defaultBaseURL = "http://localhost:11434"
	defaultModel   = "llama3.2"
	defaultTimeout = 120 * time.Second
)

// Config holds the Ollama provider configuration
type Config struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

// Provider implements the providers.Provider interface for Ollama
type Provider struct {
	config     Config
	httpClient *http.Client
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents a chat completion request
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// ChatResponse represents a chat completion response
type ChatResponse struct {
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	Message   Message   `json:"message"`
	Done      bool      `json:"done"`
}

// New creates a new Ollama provider with the given configuration
func New(config Config) (*Provider, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if config.Model == "" {
		config.Model = defaultModel
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}

	return &Provider{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

// Name returns the name of this provider
func (p *Provider) Name() string {
	return "ollama"
}

// Chat completes a chat conversation with Ollama
func (p *Provider) Chat(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("no messages provided")
	}

	// Convert providers.Messages to Ollama Messages
	messages := make([]Message, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	ollamaReq := ChatRequest{
		Model:    p.config.Model,
		Messages: messages,
		Stream:   false,
	}

	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", p.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &providers.ChatResponse{
		Content:      ollamaResp.Message.Content,
		FinishReason: "stop",
		Usage: providers.Usage{
			PromptTokens:     0, // Ollama doesn't provide token counts
			CompletionTokens: 0,
			TotalTokens:      0,
		},
	}, nil
}

// StreamChat streams a chat response from Ollama
func (p *Provider) StreamChat(ctx context.Context, req *providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("no messages provided")
	}

	messages := make([]Message, len(req.Messages))
	for i, msg := range req.Messages {
		messages[i] = Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	ollamaReq := ChatRequest{
		Model:    p.config.Model,
		Messages: messages,
		Stream:   true,
	}

	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", p.config.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	chunkChan := make(chan providers.StreamChunk, 10)

	go func() {
		defer resp.Body.Close()
		defer close(chunkChan)

		decoder := json.NewDecoder(resp.Body)
		for {
			var ollamaResp ChatResponse
			if err := decoder.Decode(&ollamaResp); err != nil {
				if err == io.EOF {
					return
				}
				chunkChan <- providers.StreamChunk{Error: fmt.Errorf("decode error: %w", err)}
				return
			}

			if ollamaResp.Done {
				chunkChan <- providers.StreamChunk{
					Done: true,
				}
				return
			}

			chunkChan <- providers.StreamChunk{
				Content: ollamaResp.Message.Content,
			}
		}
	}()

	return chunkChan, nil
}

// ListModels returns available models from Ollama
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	url := fmt.Sprintf("%s/api/tags", p.config.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	models := make([]string, len(result.Models))
	for i, m := range result.Models {
		models[i] = m.Name
	}

	return models, nil
}
