package qwen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewClient_DefaultsFromEnv(t *testing.T) {
	t.Setenv("AI_API_URL", "")
	t.Setenv("AI_MODEL", "")
	t.Setenv("DO_AI_ACCESS_KEY", "")

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if client.baseURL != defaultBaseURL {
		t.Errorf("expected baseURL %s, got %s", defaultBaseURL, client.baseURL)
	}

	if client.model != defaultModel {
		t.Errorf("expected model %s, got %s", defaultModel, client.model)
	}

	if client.apiKey != "" {
		t.Errorf("expected empty apiKey, got %s", client.apiKey)
	}
}

func TestNewClient_EnvironmentOverrides(t *testing.T) {
	t.Setenv("AI_API_URL", "https://override.example.com/")
	t.Setenv("AI_MODEL", "custom-model")
	t.Setenv("DO_AI_ACCESS_KEY", "secret")

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if client.baseURL != "https://override.example.com" {
		t.Errorf("expected trimmed baseURL, got %s", client.baseURL)
	}
	if client.model != "custom-model" {
		t.Errorf("expected model custom-model, got %s", client.model)
	}
	if client.apiKey != "secret" {
		t.Errorf("expected apiKey secret, got %s", client.apiKey)
	}
}

func TestAnalyzeTranscript_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp := chatResponse{
			Choices: []choice{
				{
					Message: message{
						Role:    "assistant",
						Content: `{"is_command":true,"intent":"request_channel_list","reply":"","channels":[],"state":"sin_canal"}`,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		model:      "test-model",
	}

	ctx := context.Background()
	result, err := client.AnalyzeTranscript(ctx, " tráeme la lista de canales ", []string{"canal-1"}, "sin_canal", "")
	if err != nil {
		t.Fatalf("AnalyzeTranscript returned error: %v", err)
	}

	if !result.IsCommand {
		t.Fatal("expected command")
	}

	if result.Intent != "request_channel_list" {
		t.Errorf("expected intent request_channel_list, got %s", result.Intent)
	}
}

func TestAnalyzeTranscript_MarkdownJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []choice{
				{
					Message: message{
						Role:    "assistant",
						Content: "```json\n{\"is_command\":false,\"intent\":\"conversation\",\"reply\":\"hola\",\"channels\":[],\"state\":\"canal-1\"}\n```",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		model:      "test-model",
	}

	result, err := client.AnalyzeTranscript(context.Background(), "hola", nil, "canal-1", "")
	if err != nil {
		t.Fatalf("AnalyzeTranscript returned error: %v", err)
	}
	if result.Intent != "conversation" || result.IsCommand {
		t.Errorf("expected conversation fallback, got %+v", result)
	}
}

func TestAnalyzeTranscript_EmptyTranscript(t *testing.T) {
	client := &Client{}
	_, err := client.AnalyzeTranscript(context.Background(), "   ", nil, "state", "")
	if err != ErrEmptyTranscript {
		t.Errorf("expected ErrEmptyTranscript, got %v", err)
	}
}

func TestAnalyzeTranscript_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		model:      "test-model",
	}

	// Use the fallback mechanism as the primary expectation
	result, err := client.AnalyzeTranscript(context.Background(), "dame la lista de canales", nil, "state", "")
	if err != nil {
		t.Fatalf("AnalyzeTranscript should not return error on HTTP error with fallback, but got: %v", err)
	}
	if !result.IsCommand || result.Intent != "request_channel_list" {
		t.Errorf("Expected fallback to detect command, but it didn't. Got: %+v", result)
	}
}

func TestAnalyzeTranscript_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []choice{
				{
					Message: message{
						Role:    "assistant",
						Content: "not-json",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		model:      "test-model",
	}

	// Expect fallback to kick in
	result, err := client.AnalyzeTranscript(context.Background(), "dame la lista de canales", nil, "state", "")
	if err != nil {
		t.Fatalf("AnalyzeTranscript should not return error on invalid JSON with fallback, but got: %v", err)
	}
	if !result.IsCommand || result.Intent != "request_channel_list" {
		t.Errorf("Expected fallback to detect command, but it didn't. Got: %+v", result)
	}
}

func TestAnalyzeTranscript_InvalidIntent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []choice{
				{
					Message: message{
						Role:    "assistant",
						Content: `{"is_command":true,"intent":"foobar","reply":"","channels":[],"state":"sin_canal"}`,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		model:      "test-model",
	}

	result, err := client.AnalyzeTranscript(context.Background(), "hola", nil, "sin_canal", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsCommand || result.Intent != "conversation" {
		t.Errorf("expected forced conversation, got %+v", result)
	}
}

func TestExtractJSONFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain json", `{"a":1}`, `{"a":1}`},
		{"markdown block", "```json\n{\n  \"a\": 1\n}\n```", "{\n  \"a\": 1\n}"},
		{"json in line", "respuesta:\n{\"a\":1}\nfinal", `{"a":1}`},
		{"fallback to original", "no json here", "no json here"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractJSONFromResponse(tt.input); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestBuildAnalysisPrompt(t *testing.T) {
	prompt := buildAnalysisPrompt("hola", []string{"canal-1", "canal-2"}, "sin_canal", "canal-3")
	if !strings.Contains(prompt, "\"hola\"") {
		t.Error("prompt missing transcript")
	}
	if !strings.Contains(prompt, "Canales disponibles: canal-1, canal-2") {
		t.Error("prompt missing channels")
	}
	if !strings.Contains(prompt, "Estado actual: sin_canal") {
		t.Error("prompt missing state")
	}
	if !strings.Contains(prompt, "Canal pendiente: canal-3") {
		t.Error("prompt missing pending channel")
	}
}

func TestAnalyzeTranscript_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(server.Close)

	client := &Client{
		httpClient: &http.Client{Timeout: 50 * time.Millisecond},
		baseURL:    server.URL,
		model:      "test-model",
	}

	// Expect fallback to kick in after timeout
	result, err := client.AnalyzeTranscript(context.Background(), "dame la lista de canales", nil, "state", "")
	if err != nil {
		t.Fatalf("AnalyzeTranscript should not return error on timeout with fallback, but got: %v", err)
	}
	if !result.IsCommand || result.Intent != "request_channel_list" {
		t.Errorf("Expected fallback to detect command after timeout, but it didn't. Got: %+v", result)
	}
}

func TestDetectCommandFallback(t *testing.T) {
	tests := []struct {
		name              string
		transcript        string
		availableChannels []string
		expectedIntent    string
		expectedChannel   string
		expectedOK        bool
	}{
		{
			name:           "list channels",
			transcript:     "dame la lista de canales",
			expectedIntent: "request_channel_list",
			expectedOK:     true,
		},
		{
			name:           "disconnect",
			transcript:     "desconéctame del canal",
			expectedIntent: "request_channel_disconnect",
			expectedOK:     true,
		},
		{
			name:              "connect with number",
			transcript:        "conéctame al canal 2",
			availableChannels: []string{"canal-1", "canal-2"},
			expectedIntent:    "request_channel_connect",
			expectedChannel:   "canal-2",
			expectedOK:        true,
		},
		{
			name:              "connect with word number",
			transcript:        "conéctame al canal dos",
			availableChannels: []string{"canal-1", "canal-2"},
			expectedIntent:    "request_channel_connect",
			expectedChannel:   "canal-2",
			expectedOK:        true,
		},
		{
			name:              "connect to unavailable channel",
			transcript:        "conéctame al canal 99",
			availableChannels: []string{"canal-1", "canal-2"},
			expectedOK:        false, // Fails validation
		},
		{
			name:       "no command",
			transcript: "hola que tal",
			expectedOK: false,
		},
		{
			name:       "connect without number",
			transcript: "conéctame a un canal",
			expectedOK: false, // No channel number extracted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := detectCommandFallback(tt.transcript, tt.availableChannels, "sin_canal")

			assert.Equal(t, tt.expectedOK, ok)

			if tt.expectedOK {
				assert.True(t, result.IsCommand)
				assert.Equal(t, tt.expectedIntent, result.Intent)
				if tt.expectedChannel != "" {
					assert.Len(t, result.Channels, 1)
					assert.Equal(t, tt.expectedChannel, result.Channels[0])
				}
			}
		})
	}
}
