package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultModel   = "deepseek-r1:7b"
	defaultBaseURL = "http://deepseek:11434"
	systemPrompt   = `Eres un asistente de walkie-talkie. Analiza la transcripción de voz y decide si es COMANDO o conversación.

COMANDOS:
- Lista de canales (intents: request_channel_list, confirm_channel_list)
- Conectarse a canal (request_channel_connect, confirm_channel_connect) con patrones "conéctame al canal-X", "cambiar al canal-X", etc.
- Salir del canal actual (request_channel_disconnect, confirm_channel_disconnect) con frases "salir del canal", "quitarme del canal".
- Confirmaciones positivas: "sí", "yes", "claro", etc.
- Negaciones: "no", "cancelar".

ESTADOS:
- sin_canal: usuario sin canal asignado.
- <canal>: código del canal actual (ej. canal-1).

RESPONDE SIEMPRE EN JSON válido:
{
  "is_command": bool,
  "intent": "nombre_del_intent",
  "reply": "texto en español",
  "channels": ["opcional canal"],
  "state": "estado",
  "pending_channel": "opcional"
}

Si no es comando, marca is_command=false e intento "conversation".`
)

type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
}

type CommandResult struct {
	IsCommand      bool     `json:"is_command"`
	Intent         string   `json:"intent"`
	Reply          string   `json:"reply"`
	Channels       []string `json:"channels,omitempty"`
	State          string   `json:"state"`
	PendingChannel string   `json:"pending_channel,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatResponse struct {
	Message message `json:"message"`
}

var ErrEmptyTranscript = errors.New("deepseek: transcripción vacía")

func NewClient() (*Client, error) {
	baseURL := strings.TrimSpace(os.Getenv("DEEPSEEK_API_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	model := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = defaultModel
	}
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))

	return &Client{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
	}, nil
}

func (c *Client) AnalyzeTranscript(ctx context.Context, transcript string, channels []string, currentState string, pendingChannel string) (CommandResult, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return CommandResult{}, ErrEmptyTranscript
	}

	fallback := CommandResult{
		IsCommand: false,
		Intent:    "conversation",
		Reply:     transcript,
		State:     currentState,
	}

	userPrompt := buildAnalysisPrompt(transcript, channels, currentState, pendingChannel)

	reqBody := chatRequest{
		Model:  c.model,
		Stream: false,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fallback, fmt.Errorf("deepseek: serialize request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fallback, fmt.Errorf("deepseek: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fallback, fmt.Errorf("deepseek: request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fallback, fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, string(body))
	}

	var decoded chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fallback, fmt.Errorf("deepseek: parse response: %w", err)
	}

	content := strings.TrimSpace(decoded.Message.Content)
	if content == "" {
		return fallback, errors.New("deepseek: respuesta vacía")
	}

	var result CommandResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return fallback, fmt.Errorf("deepseek: json inválido: %w", err)
	}
	return result, nil
}

func buildAnalysisPrompt(transcript string, channels []string, currentState string, pendingChannel string) string {
	var sb strings.Builder
	sb.WriteString("Texto transcrito: ")
	sb.WriteString(strconvJSONString(transcript))
	sb.WriteRune('\n')

	sb.WriteString("Estado actual: ")
	sb.WriteString(currentState)
	sb.WriteRune('\n')

	if pendingChannel != "" {
		sb.WriteString("Canal pendiente: ")
		sb.WriteString(pendingChannel)
		sb.WriteRune('\n')
	}

	if len(channels) == 0 {
		sb.WriteString("Canales disponibles: ninguno\n")
	} else {
		sb.WriteString("Canales disponibles: ")
		sb.WriteString(strings.Join(channels, ", "))
		sb.WriteRune('\n')
	}

	sb.WriteString("Devuelve JSON siguiendo el formato indicado.")
	return sb.String()
}

func strconvJSONString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
