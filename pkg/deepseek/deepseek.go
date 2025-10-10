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
	defaultModel   = "deepseek-chat"
	defaultBaseURL = "https://api.deepseek.com/v1"
	systemPrompt   = `Eres Gopebot, asistente de walkie-talkie para un equipo móvil.
Debes interpretar comandos de voz ya transcritos al texto.
Usa solo los datos provistos por el backend para responder.
Responde SIEMPRE en español usando JSON con esta forma:
{"reply":"texto a devolver al usuario","intent":"nombre_intencion","channels":["lista","opcional"]}.
Si no entiendes el comando, usa intent "unknown" y explica que no lo comprendiste.`
)

// Client maneja las llamadas a la API de DeepSeek.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
}

// CommandResult representa la respuesta normalizada del modelo.
type CommandResult struct {
	Reply    string   `json:"reply"`
	Intent   string   `json:"intent"`
	Channels []string `json:"channels,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequest struct {
	Model          string            `json:"model"`
	Messages       []message         `json:"messages"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type completionResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

var (
	ErrMissingAPIKey = errors.New("deepseek: DEEPSEEK_API_KEY no configurada")
)

// NewClient inicializa el cliente con la configuración del entorno.
func NewClient() (*Client, error) {
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if apiKey == "" {
		return nil, ErrMissingAPIKey
	}
	baseURL := strings.TrimSpace(os.Getenv("DEEPSEEK_API_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      defaultModel,
	}, nil
}

// ProcessCommand envía el comando y devuelve la intención/respuesta.
func (c *Client) ProcessCommand(ctx context.Context, transcript string, channels []string) (CommandResult, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return CommandResult{}, errors.New("deepseek: comando vacío")
	}

	reqBody := completionRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildUserPrompt(transcript, channels)},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: no se pudo serializar request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: no se pudo crear request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: error realizando request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return CommandResult{}, fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, string(body))
	}

	var decoded completionResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: no se pudo parsear respuesta: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return CommandResult{}, errors.New("deepseek: respuesta sin opciones")
	}

	content := strings.TrimSpace(decoded.Choices[0].Message.Content)
	var result CommandResult
	if err := json.Unmarshal([]byte(content), &result); err == nil && result.Reply != "" {
		return result, nil
	}

	// Fallback: devolver texto plano.
	return CommandResult{
		Reply:  content,
		Intent: "raw_response",
	}, nil
}

func buildUserPrompt(transcript string, channels []string) string {
	var sb strings.Builder
	sb.WriteString("Comando transcrito del usuario: ")
	sb.WriteString(strconvJSONString(transcript))
	sb.WriteRune('\n')
	if len(channels) == 0 {
		sb.WriteString("No hay canales públicos registrados actualmente.\n")
	} else {
		sb.WriteString("Canales públicos disponibles: ")
		sb.WriteString(strings.Join(channels, ", "))
		sb.WriteRune('\n')
	}
	sb.WriteString("Indica intent según el comando. Usa channels solo si el intent es list_channels u otro que requiera datos.")
	return sb.String()
}

func strconvJSONString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
