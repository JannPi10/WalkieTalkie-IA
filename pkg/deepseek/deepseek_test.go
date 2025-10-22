package deepseek

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewClient_DefaultsFromEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_URL", "")
	t.Setenv("DEEPSEEK_MODEL", "")
	t.Setenv("DEEPSEEK_API_KEY", "")

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if client.baseURL != defaultBaseURL {
		t.Fatalf("expected baseURL %s, got %s", defaultBaseURL, client.baseURL)
	}

	if client.model != defaultModel {
		t.Fatalf("expected model %s, got %s", defaultModel, client.model)
	}

	if client.apiKey != "" {
		t.Fatalf("expected empty apiKey, got %s", client.apiKey)
	}
}

func TestAnalyzeTranscript_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}

		resp := `{"message":{"role":"assistant","content":"{\"is_command\":true,\"intent\":\"request_channel_list\",\"reply\":\"\",\"channels\":[],\"state\":\"sin_canal\"}"}}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
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
		t.Fatalf("expected command")
	}

	if result.Intent != "request_channel_list" {
		t.Fatalf("expected intent request_channel_list, got %s", result.Intent)
	}
}

func TestAnalyzeTranscript_MarkdownJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := "{\"message\":{\"role\":\"assistant\",\"content\":\"```json\\n{\\\"is_command\\\":false,\\\"intent\\\":\\\"conversation\\\",\\\"reply\\\":\\\"hola\\\",\\\"channels\\\":[],\\\"state\\\":\\\"canal-1\\\"}\\n```\"}}"
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
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
		t.Fatalf("expected conversation fallback, got %+v", result)
	}
}

func TestAnalyzeTranscript_EmptyTranscript(t *testing.T) {
	client := &Client{}
	_, err := client.AnalyzeTranscript(context.Background(), "   ", nil, "state", "")
	if err != ErrEmptyTranscript {
		t.Fatalf("expected ErrEmptyTranscript, got %v", err)
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

	_, err := client.AnalyzeTranscript(context.Background(), "hola", nil, "state", "")
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestAnalyzeTranscript_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{"message":{"role":"assistant","content":"not-json"}}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)

	client := &Client{
		httpClient: server.Client(),
		baseURL:    server.URL,
		model:      "test-model",
	}

	_, err := client.AnalyzeTranscript(context.Background(), "hola", nil, "state", "")
	if err == nil || !strings.Contains(err.Error(), "json inválido") {
		t.Fatalf("expected json error, got %v", err)
	}
}

func TestAnalyzeTranscript_InvalidIntent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{"message":{"role":"assistant","content":"{\"is_command\":true,\"intent\":\"foobar\",\"reply\":\"\",\"channels\":[],\"state\":\"sin_canal\"}"}}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(resp))
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
		t.Fatalf("expected forced conversation, got %+v", result)
	}
}

func TestExtractJSONFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain json",
			input:    `{"a":1}`,
			expected: `{"a":1}`,
		},
		{
			name:     "markdown block",
			input:    "```json\n{\n  \"a\": 1\n}\n```",
			expected: "{\n  \"a\": 1\n}",
		},
		{
			name:     "json in line",
			input:    "respuesta:\n{\"a\":1}\nfinal",
			expected: `{"a":1}`,
		},
		{
			name:     "fallback to original",
			input:    "no json here",
			expected: "no json here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractJSONFromResponse(tt.input); got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestBuildAnalysisPrompt(t *testing.T) {
	prompt := buildAnalysisPrompt("hola", []string{"canal-1", "canal-2"}, "sin_canal", "canal-3")
	if !strings.Contains(prompt, "hola") {
		t.Fatalf("prompt missing transcript")
	}
	if !strings.Contains(prompt, "canal-1, canal-2") {
		t.Fatalf("prompt missing channels")
	}
	if !strings.Contains(prompt, "sin_canal") {
		t.Fatalf("prompt missing state")
	}
	if !strings.Contains(prompt, "canal pendiente: canal-3") && !strings.Contains(strings.ToLower(prompt), "canal pendiente") {
		t.Fatalf("prompt missing pending channel")
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

	_, err := client.AnalyzeTranscript(context.Background(), "hola", nil, "state", "")
	if err == nil || !strings.Contains(err.Error(), "request error") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestNewClient_EnvironmentOverrides(t *testing.T) {
	t.Setenv("DEEPSEEK_API_URL", "https://override.example.com/")
	t.Setenv("DEEPSEEK_MODEL", "custom-model")
	t.Setenv("DEEPSEEK_API_KEY", "secret")

	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if client.baseURL != "https://override.example.com" {
		t.Fatalf("expected trimmed baseURL, got %s", client.baseURL)
	}
	if client.model != "custom-model" {
		t.Fatalf("expected model custom-model, got %s", client.model)
	}
	if client.apiKey != "secret" {
		t.Fatalf("expected apiKey secret, got %s", client.apiKey)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
