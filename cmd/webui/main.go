// Privacy-First PicoClaw Web UI
// Simple, local-only web interface for PicoClaw

package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

//go:embed static
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow local connections only
	},
}

// ChatMessage represents a chat message
type ChatMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// ChatRequest represents a chat request from the client
type ChatRequest struct {
	Messages     []ChatMessage `json:"messages"`
	Provider     string        `json:"provider"`
	Model        string        `json:"model"`
	SystemPrompt string        `json:"systemPrompt"`
	SessionKey   string        `json:"sessionKey"`
}

// ChatResponse represents a streaming chunk response
type ChatResponse struct {
	Content string `json:"content"`
	Done    bool   `json:"done"`
	Error   string `json:"error,omitempty"`
}

// ModelsResponse lists available models
type ModelsResponse struct {
	Provider string   `json:"provider"`
	Models   []string `json:"models"`
}

// SessionInfo represents a session for the frontend
type SessionInfo struct {
	Key      string `json:"key"`
	Messages int    `json:"messages"`
	Updated  int64  `json:"updated"`
}

// ProviderWrapper wraps LLMProvider with additional metadata
type ProviderWrapper struct {
	name        string
	provider    providers.LLMProvider
	listModelsFn func(ctx context.Context) ([]string, error)
}

func (p *ProviderWrapper) Name() string {
	return p.name
}

func (p *ProviderWrapper) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	return p.provider.Chat(ctx, messages, tools, model, options)
}

func (p *ProviderWrapper) StreamChat(ctx context.Context, req *StreamChatRequest) (<-chan StreamChunk, error) {
	// For non-streaming providers, simulate streaming
	chunkChan := make(chan StreamChunk, 1)
	go func() {
		defer close(chunkChan)

		messages := req.Messages
		resp, err := p.provider.Chat(ctx, messages, nil, req.Model, nil)
		if err != nil {
			chunkChan <- StreamChunk{Error: err}
			return
		}

		// Send the full response as one chunk
		chunkChan <- StreamChunk{
			Content: resp.Content,
			Done:    true,
		}
	}()
	return chunkChan, nil
}

func (p *ProviderWrapper) ListModels(ctx context.Context) ([]string, error) {
	if p.listModelsFn != nil {
		return p.listModelsFn(ctx)
	}
	return []string{p.provider.GetDefaultModel()}, nil
}

func (p *ProviderWrapper) GetDefaultModel() string {
	return p.provider.GetDefaultModel()
}

// StreamChatRequest represents a streaming chat request
type StreamChatRequest struct {
	Messages []providers.Message
	Model    string
}

// StreamChunk represents a chunk of streamed response
type StreamChunk struct {
	Content string
	Done    bool
	Error   error
}

var (
	cfg              *config.Config
	agentLoop        *agent.AgentLoop
	msgBus           *bus.MessageBus
	sessions         *session.SessionManager
	providerMap      = make(map[string]*ProviderWrapper)
	mu               sync.RWMutex
	sessionStoragePath string
)

func main() {
	// Default port
	port := "8080"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	// Load configuration
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".picoclaw", "config.json")
	var err error
	cfg, err = config.LoadConfig(configPath)
	if err != nil {
		log.Printf("Warning: Could not load config: %v", err)
		cfg = config.DefaultConfig()
	}

	// Initialize message bus
	msgBus = bus.NewMessageBus()

	// Initialize session manager with storage path
	sessionStoragePath = filepath.Join(home, ".picoclaw", "sessions")
	sessions = session.NewSessionManager(sessionStoragePath)
	log.Printf("Session storage: %s", sessionStoragePath)

	// Initialize providers
	initializeProviders()

	// Initialize agent loop with default provider
	defaultProvider := getDefaultProvider()
	if defaultProvider == nil {
		log.Fatal("No provider available. Please configure API keys in config.json or run Ollama locally")
	}

	agentLoop = agent.NewAgentLoop(cfg, msgBus, defaultProvider.provider)

	// Setup HTTP routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/models", handleModels)
	http.HandleFunc("/api/sessions", handleSessions)
	http.HandleFunc("/api/sessions/", handleSessionDetail)
	http.HandleFunc("/ws", handleWebSocket)

	// Serve static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Start server
	addr := ":" + port
	log.Printf("\U0001F310 Privacy-First PicoClaw Web UI starting on http://localhost%s", addr)
	log.Println("Press Ctrl+C to stop")

	server := &http.Server{
		Addr:         addr,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// Graceful shutdown
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\nShutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	log.Println("Server stopped")
}

func initializeProviders() {
	// Initialize Ollama (primary local provider)
	ollamaBaseURL := "http://localhost:11434"
	if cfg.Providers.VLLM.APIBase != "" {
		ollamaBaseURL = cfg.Providers.VLLM.APIBase
	}

	ollamaProvider, err := providers.CreateOllamaProvider(ollamaBaseURL)
	if err == nil {
		// Test connection
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if ollamaLister, ok := ollamaProvider.(interface{ ListModels(context.Context) ([]string, error) }); ok {
			models, _ := ollamaLister.ListModels(ctx)
			cancel()
			if models != nil && len(models) > 0 {
				wrapper := &ProviderWrapper{
					name:    "ollama",
					provider: ollamaProvider,
					listModelsFn: func(ctx context.Context) ([]string, error) {
						return ollamaLister.ListModels(ctx)
					},
				}
				providerMap["ollama"] = wrapper
				log.Printf("\u2713 Ollama provider initialized (%d models available)", len(models))
			} else {
				log.Println("\u26A0 Ollama provider configured but not reachable - make sure Ollama is running")
			}
		} else {
			cancel()
		}
	} else {
		log.Printf("\u26A0 Failed to initialize Ollama provider: %v", err)
	}

	// List available providers
	available := getAvailableProviders()
	if len(available) == 0 {
		log.Println("\u26A0 No providers configured. Start Ollama with: ollama serve")
	} else {
		log.Printf("Available providers: %v", available)
	}
}

func getAvailableProviders() []string {
	mu.RLock()
	defer mu.RUnlock()

	var provs []string
	for name := range providerMap {
		provs = append(provs, name)
	}
	return provs
}

func getProvider(name string) *ProviderWrapper {
	mu.RLock()
	defer mu.RUnlock()
	return providerMap[name]
}

func getDefaultProvider() *ProviderWrapper {
	mu.RLock()
	defer mu.RUnlock()

	// Priority: Ollama only for MVP
	priority := []string{"ollama"}
	for _, name := range priority {
		if p, ok := providerMap[name]; ok {
			return p
		}
	}
	return nil
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	content, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(content)
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Get or generate session key
	sessionKey := req.SessionKey
	if sessionKey == "" {
		sessionKey = "webui:" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}

	// Get provider
	providerName := req.Provider
	if providerName == "" {
		if p := getDefaultProvider(); p != nil {
			providerName = p.Name()
		}
	}

	provider := getProvider(providerName)
	if provider == nil {
		http.Error(w, fmt.Sprintf("Provider '%s' not available", providerName), http.StatusBadRequest)
		return
	}

	// Load session history
	history := sessions.GetHistory(sessionKey)

	// Convert new messages and append to history
	for _, msg := range req.Messages {
		history = append(history, providers.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
		// Save to session
		sessions.AddMessage(sessionKey, msg.Role, msg.Content)
	}

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		history = append([]providers.Message{
			{Role: "system", Content: req.SystemPrompt},
		}, history...)
	}

	// Determine model
	model := req.Model
	if model == "" {
		model = provider.GetDefaultModel()
	}

	chatReq := &StreamChatRequest{
		Messages: history,
		Model:    model,
	}

	// Stream response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	chunkChan, err := provider.StreamChat(ctx, chatReq)
	if err != nil {
		fmt.Fprintf(w, "data: {\"error\": \"%s\"}\n\n", err.Error())
		flusher.Flush()
		return
	}

	var fullResponse string
	for chunk := range chunkChan {
		if chunk.Error != nil {
			fmt.Fprintf(w, "data: {\"error\": \"%s\"}\n\n", chunk.Error.Error())
			flusher.Flush()
			break
		}

		response := ChatResponse{
			Content: chunk.Content,
			Done:    chunk.Done,
		}

		data, _ := json.Marshal(response)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		fullResponse += chunk.Content

		if chunk.Done {
			// Save assistant response to session
			if fullResponse != "" {
				sessions.AddMessage(sessionKey, "assistant", fullResponse)
				// Persist session
				_ = sessions.Save(sessionKey)
			}
			break
		}
	}
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	providerName := r.URL.Query().Get("provider")
	if providerName == "" {
		providerName = "all"
	}

	var result []ModelsResponse

	if providerName == "all" {
		for name, p := range providerMap {
			models, err := p.ListModels(context.Background())
			if err != nil {
				models = []string{"default"}
			}
			result = append(result, ModelsResponse{
				Provider: name,
				Models:   models,
			})
		}
	} else {
		provider := getProvider(providerName)
		if provider == nil {
			http.Error(w, "Provider not found", http.StatusNotFound)
			return
		}

		models, err := provider.ListModels(context.Background())
		if err != nil {
			models = []string{"default"}
		}

		result = append(result, ModelsResponse{
			Provider: providerName,
			Models:   models,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleSessions returns list of all sessions
func handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	entries, err := os.ReadDir(sessionStoragePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]SessionInfo{})
		return
	}

	var sessionList []SessionInfo
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		// Read session file to get info
		sessionPath := filepath.Join(sessionStoragePath, entry.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}

		var sess session.Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		sessionList = append(sessionList, SessionInfo{
			Key:      sess.Key,
			Messages: len(sess.Messages),
			Updated:  sess.Updated.Unix(),
		})
	}

	// Sort by updated time (newest first)
	sort.Slice(sessionList, func(i, j int) bool {
		return sessionList[i].Updated > sessionList[j].Updated
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessionList)
}

// handleSessionDetail handles loading or deleting a specific session
func handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	// Extract session key from URL path
	// URL format: /api/sessions/{key}
	path := r.URL.Path[len("/api/sessions/"):]
	if path == "" {
		http.Error(w, "Session key required", http.StatusBadRequest)
		return
	}

	// Handle DELETE
	if r.Method == http.MethodDelete {
		// Use the SessionManager's Delete method to properly remove the session
		// from both memory and disk with correct filename sanitization
		deleted := sessions.Delete(path)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"success": deleted})
		return
	}

	// Handle GET - load session messages
	if r.Method == http.MethodGet {
		history := sessions.GetHistory(path)

		var messages []ChatMessage
		for _, msg := range history {
			messages = append(messages, ChatMessage{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(messages)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	log.Println("WebSocket client connected")

	for {
		var req ChatRequest
		if err := conn.ReadJSON(&req); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Get or generate session key
		sessionKey := req.SessionKey
		if sessionKey == "" {
			sessionKey = "webui:" + strconv.FormatInt(time.Now().UnixNano(), 36)
		}

		// Get provider
		providerName := req.Provider
		if providerName == "" {
			if p := getDefaultProvider(); p != nil {
				providerName = p.Name()
			}
		}

		provider := getProvider(providerName)
		if provider == nil {
			conn.WriteJSON(ChatResponse{Error: fmt.Sprintf("Provider '%s' not available", providerName)})
			continue
		}

		// Load session history
		history := sessions.GetHistory(sessionKey)

		// Convert new messages and append to history
		for _, msg := range req.Messages {
			history = append(history, providers.Message{
				Role:    msg.Role,
				Content: msg.Content,
			})
			// Save to session
			sessions.AddMessage(sessionKey, msg.Role, msg.Content)
		}

		if req.SystemPrompt != "" {
			history = append([]providers.Message{
				{Role: "system", Content: req.SystemPrompt},
			}, history...)
		}

		model := req.Model
		if model == "" {
			model = provider.GetDefaultModel()
		}

		chatReq := &StreamChatRequest{
			Messages: history,
			Model:    model,
		}

		ctx := context.Background()
		chunkChan, err := provider.StreamChat(ctx, chatReq)
		if err != nil {
			conn.WriteJSON(ChatResponse{Error: err.Error()})
			continue
		}

		var fullResponse string
		for chunk := range chunkChan {
			if chunk.Error != nil {
				conn.WriteJSON(ChatResponse{Error: chunk.Error.Error()})
				break
			}

			if err := conn.WriteJSON(ChatResponse{
				Content: chunk.Content,
				Done:    chunk.Done,
			}); err != nil {
				log.Printf("WebSocket write error: %v", err)
				break
			}

			fullResponse += chunk.Content

			if chunk.Done {
				// Save assistant response to session
				if fullResponse != "" {
					sessions.AddMessage(sessionKey, "assistant", fullResponse)
					// Persist session
					_ = sessions.Save(sessionKey)
				}
				break
			}
		}
	}

	log.Println("WebSocket client disconnected")
}
