// PicoClaw - Ultra-lightweight personal AI agent
// Ollama Provider for local LLM support
// Copyright (c) 2026 PicoClaw contributors

package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	DefaultOllamaBaseURL = "http://localhost:11434"
	DefaultOllamaModel   = "llama3.2"
)

// OllamaConfig holds the Ollama provider configuration
type OllamaConfig struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

// OllamaProvider implements LLMProvider for Ollama
type OllamaProvider struct {
	config     OllamaConfig
	httpClient *http.Client
}

// OllamaMessage represents a message in Ollama format
type OllamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OllamaRequest represents a chat request to Ollama
type OllamaRequest struct {
	Model    string          `json:"model"`
	Messages []OllamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

// OllamaResponse represents a response from Ollama
type OllamaResponse struct {
	Model     string       `json:"model"`
	CreatedAt time.Time    `json:"created_at"`
	Message   OllamaMessage `json:"message"`
	Done      bool         `json:"done"`
}

// CreateOllamaProvider creates a new Ollama provider
func CreateOllamaProvider(baseURL string) (LLMProvider, error) {
	config := OllamaConfig{
		BaseURL: baseURL,
		Model:   DefaultOllamaModel,
		Timeout: 120 * time.Second,
	}
	
	if config.BaseURL == "" {
		config.BaseURL = DefaultOllamaBaseURL
	}

	return &OllamaProvider{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

// Chat completes a chat conversation with Ollama
func (p *OllamaProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("no messages provided")
	}

	// Convert to Ollama format
	ollamaMessages := make([]OllamaMessage, len(messages))
	for i, msg := range messages {
		ollamaMessages[i] = OllamaMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// Use provided model or default
	if model == "" {
		model = p.config.Model
	}

	ollamaReq := OllamaRequest{
		Model:    model,
		Messages: ollamaMessages,
		Stream:   false,
	}

	reqBody, err := json.Marshal(ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", p.config.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &LLMResponse{
		Content:      ollamaResp.Message.Content,
		FinishReason: "stop",
		Usage: &UsageInfo{
			PromptTokens:     0, // Ollama doesn't provide token counts
			CompletionTokens: 0,
			TotalTokens:      0,
		},
	}, nil
}

// GetDefaultModel returns the default model for Ollama
func (p *OllamaProvider) GetDefaultModel() string {
	return p.config.Model
}

// ListModels returns available models from Ollama
func (p *OllamaProvider) ListModels(ctx context.Context) ([]string, error) {
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
