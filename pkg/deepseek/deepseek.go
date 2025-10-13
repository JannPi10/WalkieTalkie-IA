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
	systemPrompt   = `Eres Gopebot, asistente de walkie-talkie para un equipo móvil.
Debes interpretar comandos de voz ya transcritos al texto.
Usa solo los datos provistos por el backend para responder.
Responde SIEMPRE en español usando JSON con esta forma:
{"reply":"texto a devolver al usuario","intent":"nombre_intencion","channels":["lista","opcional"]}.
Si no entiendes el comando, usa intent "unknown" y explica que no lo comprendiste.`
)

// Client maneja las llamadas al servicio local (u oficial) de DeepSeek/Ollama.
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

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatResponse struct {
	Message message `json:"message"`
}

var ErrEmptyCommand = errors.New("deepseek: comando vacío")

// NewClient inicializa el cliente usando variables de entorno.
func NewClient() (*Client, error) {
	baseURL := strings.TrimSpace(os.Getenv("DEEPSEEK_API_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	model := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = defaultModel
	}
	// apiKey es opcional. Solo se envía si está presente.
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))

	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
	}, nil
}

// ProcessCommand envía el comando y devuelve la respuesta del modelo.
func (c *Client) ProcessCommand(ctx context.Context, transcript string, channels []string) (CommandResult, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return CommandResult{}, ErrEmptyCommand
	}

	reqBody := chatRequest{
		Model:  c.model,
		Stream: false,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildUserPrompt(transcript, channels)},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: no se pudo serializar request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: no se pudo crear request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: error realizando request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return CommandResult{}, fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, string(body))
	}

	var decoded chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return CommandResult{}, fmt.Errorf("deepseek: no se pudo parsear respuesta: %w", err)
	}

	content := strings.TrimSpace(decoded.Message.Content)
	if content == "" {
		return CommandResult{}, errors.New("deepseek: respuesta vacía")
	}

	var result CommandResult
	if err := json.Unmarshal([]byte(content), &result); err == nil && result.Reply != "" {
		return result, nil
	}

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
	sb.WriteString("Indica intent según el comando. Usa channels solo si corresponde.")
	return sb.String()
}

func strconvJSONString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
