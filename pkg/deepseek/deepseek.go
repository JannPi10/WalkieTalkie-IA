package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultModel   = "qwen2.5:0.5b"
	defaultBaseURL = "http://deepseek:11434"
	systemPrompt   = `Eres un asistente de walkie-talkie que analiza transcripciones de voz en español para determinar si son COMANDOS o conversación normal.

COMANDOS VÁLIDOS:
1. LISTAR CANALES:
   - "tráeme la lista de canales"
   - "dame la lista de canales" 
   - "cuáles son los canales"
   - "qué canales hay"
   - "lista de canales"
   - "canales disponibles"

2. CONECTAR A CANAL:
   - "conectarme al canal-X" (donde X es: 1, 2, 3, 4, 5)
   - "conectame al canal-X"
   - "cambiar al canal-X"
   - "ir al canal-X"
   - "entrar al canal-X"
   - "úneme al canal-X"

3. DESCONECTAR DEL CANAL:
   - "salir del canal"
   - "desconectarme del canal"
   - "quitarme del canal"
   - "abandonar el canal"

ESTADOS DEL USUARIO:
- "sin_canal": usuario no está en ningún canal
- "canal-X": usuario está en el canal X (canal-1, canal-2, etc.)

RESPUESTA REQUERIDA:
Responde SOLO con JSON válido, sin texto adicional ni markdown:
{
  "is_command": true/false,
  "intent": "nombre_del_intent",
  "reply": "respuesta en español",
  "channels": ["canal-X"] (solo si aplica),
  "state": "estado_actual",
  "pending_channel": "" (opcional)
}

INTENTS VÁLIDOS:
- "request_channel_list": para listar canales
- "request_channel_connect": para conectar a canal
- "request_channel_disconnect": para desconectar
- "conversation": para conversación normal

EJEMPLOS:
Entrada: "tráeme la lista de canales"
Salida: {"is_command": true, "intent": "request_channel_list", "reply": "Te traigo la lista de canales", "channels": [], "state": "sin_canal"}

Entrada: "conectarme al canal-1"
Salida: {"is_command": true, "intent": "request_channel_connect", "reply": "Te conecto al canal-1", "channels": ["canal-1"], "state": "sin_canal"}

Entrada: "hola cómo están"
Salida: {"is_command": false, "intent": "conversation", "reply": "hola cómo están", "channels": [], "state": "sin_canal"}

IMPORTANTE: Responde ÚNICAMENTE con el JSON, sin explicaciones ni formato markdown.`
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
		httpClient: &http.Client{Timeout: 180 * time.Second},
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

	// Extraer JSON de respuesta markdown si es necesario
	jsonContent := extractJSONFromResponse(content)

	var result CommandResult
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		// Log para debugging
		log.Printf("DEBUG: Respuesta de Deepseek: %s", content)
		log.Printf("DEBUG: JSON extraído: %s", jsonContent)
		return fallback, fmt.Errorf("deepseek: json inválido: %w", err)
	}
	return result, nil
}

// extractJSONFromResponse extrae JSON de una respuesta que puede estar en formato markdown
func extractJSONFromResponse(content string) string {
	content = strings.TrimSpace(content)

	// Si ya es JSON válido, devolverlo tal como está
	if strings.HasPrefix(content, "{") && strings.HasSuffix(content, "}") {
		return content
	}

	// Buscar JSON dentro de bloques de código markdown
	if strings.Contains(content, "```") {
		lines := strings.Split(content, "\n")
		var jsonLines []string
		inCodeBlock := false

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "```") {
				inCodeBlock = !inCodeBlock
				continue
			}
			if inCodeBlock && line != "" {
				jsonLines = append(jsonLines, line)
			}
		}

		if len(jsonLines) > 0 {
			return strings.Join(jsonLines, "\n")
		}
	}

	// Buscar líneas que contengan JSON
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			return line
		}
	}

	// Como último recurso, devolver el contenido original
	return content
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

	sb.WriteString("Responde ÚNICAMENTE con JSON válido.")
	return sb.String()
}

func strconvJSONString(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}
