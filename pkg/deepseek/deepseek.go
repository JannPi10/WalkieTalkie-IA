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
	systemPrompt   = `Eres un asistente de walkie-talkie inteligente, amigable y conversacional. Tu función es analizar texto transcrito de audio e identificar si el usuario está emitiendo un COMANDO o una CONVERSACIÓN normal.

COMANDOS A RECONOCER:

1. Solicitar Lista de Canales
Variaciones: "dame la lista", "tráeme los canales", "qué canales hay", "lista de canales"
Intent: request_channel_list

2. Conectarse a Canal
Patrón: "conectame al canal-X", "cambiar al canal-X", "ir al canal-X" (X=1-5)
Intent: request_channel_connect

3. Confirmaciones
Palabras clave: "si", "sí", "yes", "aja", "ajam", "obvio", "claro", "correcto", "exacto"
Intent: confirm_channel_list o confirm_channel_connect (según contexto)

4. Negaciones
Palabras clave: "no", "nope", "para nada", "cancelar"
Intent: deny_action

ESTADOS DE CONVERSACIÓN:
- normal: Sin comandos pendientes
- awaiting_channel_list_confirm: Esperando confirmación para mostrar lista
- awaiting_channel_connect_confirm: Esperando confirmación para conectar

INTENTS DISPONIBLES:
- request_channel_list: Usuario pide lista de canales
- confirm_channel_list: Usuario confirma ver lista
- request_channel_connect: Usuario pide conectarse a canal
- confirm_channel_connect: Usuario confirma conexión
- deny_action: Usuario cancela acción
- conversation: Conversación normal
- unknown: No se entiende

FORMATO DE RESPUESTA (JSON VÁLIDO):
{
  "is_command": true/false,
  "intent": "nombre_del_intent",
  "reply": "respuesta amigable en español",
  "channels": ["lista_opcional"],
  "state": "estado_conversacion",
  "pending_channel": "canal_pendiente_si_aplica"
}

DIRECTRICES:
- Sé conversacional y amigable
- Pide confirmación para comandos importantes
- Respuestas claras y concisas, optimizadas para síntesis de voz
- Evita caracteres especiales innecesarios
- Usa puntuación natural para mejorar entonación
- Favorece palabras comunes`
)

// Client maneja las llamadas al servicio local de DeepSeek/Ollama.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
}

// CommandResult representa la respuesta del modelo.
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
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))

	return &Client{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
	}, nil
}

// AnalyzeTranscript analiza el texto transcrito y determina si es comando o conversación
func (c *Client) AnalyzeTranscript(ctx context.Context, transcript string, channels []string, currentState string, pendingChannel string) (CommandResult, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return CommandResult{}, ErrEmptyTranscript
	}

	fallback := CommandResult{
		IsCommand: false,
		Intent:    "unknown",
		Reply:     "Lo siento, no pude procesar tu comando. ¿Puedes repetirlo?",
		State:     "normal",
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
		return fallback, fmt.Errorf("deepseek: no se pudo serializar request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fallback, fmt.Errorf("deepseek: no se pudo crear request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fallback, fmt.Errorf("deepseek: error realizando request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fallback, fmt.Errorf("deepseek: status %d: %s", resp.StatusCode, string(body))
	}

	var decoded chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fallback, fmt.Errorf("deepseek: no se pudo parsear respuesta: %w", err)
	}

	content := strings.TrimSpace(decoded.Message.Content)
	if content == "" {
		return fallback, errors.New("deepseek: respuesta vacía")
	}

	var result CommandResult
	if err := json.Unmarshal([]byte(content), &result); err == nil && result.Reply != "" {
		return result, nil
	}

	return CommandResult{
		IsCommand: false,
		Intent:    "conversation",
		Reply:     content,
		State:     "normal",
	}, nil
}

func buildAnalysisPrompt(transcript string, channels []string, currentState string, pendingChannel string) string {
	var sb strings.Builder
	sb.WriteString("Texto transcrito del usuario: ")
	sb.WriteString(strconvJSONString(transcript))
	sb.WriteRune('\n')

	sb.WriteString("Estado actual de conversación: ")
	sb.WriteString(currentState)
	sb.WriteRune('\n')

	if pendingChannel != "" {
		sb.WriteString("Canal pendiente de confirmación: ")
		sb.WriteString(pendingChannel)
		sb.WriteRune('\n')
	}

	if len(channels) == 0 {
		sb.WriteString("Canales disponibles: ninguno registrado\n")
	} else {
		sb.WriteString("Canales disponibles: ")
		sb.WriteString(strings.Join(channels, ", "))
		sb.WriteRune('\n')
	}

	sb.WriteString("Analiza si es comando o conversación y responde en JSON.")
	return sb.String()
}

func strconvJSONString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
