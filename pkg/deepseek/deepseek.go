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
	defaultModel   = "qwen2.5:1.5b"
	defaultBaseURL = "http://deepseek:11434"
	systemPrompt   = `Eres un asistente de walkie-talkie. Tu ÚNICA función es detectar COMANDOS EXPLÍCITOS de sistema.

REGLA #1: Solo marca como comando si la frase contiene LITERALMENTE las palabras clave exactas.

COMANDOS VÁLIDOS (SOLO ESTOS):

1. LISTAR CANALES - requiere TODAS estas palabras:
   ✓ "lista" Y "canales"
   ✓ "tráeme" Y "canales"
   ✓ "dame" Y "canales"
   ✓ "cuáles" Y "canales"
   ✓ "qué canales"
   ✓ "canales disponibles"

2. CONECTAR A CANAL - requiere:
   ✓ "conecta" Y número
   ✓ "conectar" Y número
   ✓ "cambiar" Y "canal" Y número
   ✓ "ir" Y "canal" Y número
   ✓ "entrar" Y "canal" Y número

3. DESCONECTAR - requiere:
   ✓ "salir" Y "canal"
   ✓ "desconectar" Y "canal"

4. LISTAR USUARIOS - requiere:
   ✓ "lista" Y "usuarios"
   ✓ "tráeme" Y "usuarios"
   ✓ "dame" Y "usuarios"
   ✓ "cuáles" Y "usuarios"
   ✓ "qué usuarios"
   ✓ "usuarios disponibles"

5. EN QUE CANAL ESTOY - requiere:
   ✓ "en qué canal estoy"
   ✓ "dime mi canal"
   ✓ "cuál es mi canal"
   ✓ "qué canal es"
   ✓ "mi canal"
   ✓ "mi canal actual"

TODO LO DEMÁS ES CONVERSACIÓN, incluyendo:
✗ Saludos: "hola", "buenos días", "qué tal"
✗ Preguntas generales: "cómo estás", "qué haces", "cómo te va"
✗ Conversación casual: cualquier frase que NO contenga las palabras clave exactas
✗ Nombres de personas: "Carlos", "María", "Juan"

EJEMPLOS DE COMANDOS:
{"is_command": true, "intent": "request_channel_list", "reply": "", "channels": [], "state": "canal-1"}
Entrada: "tráeme la lista de canales"

{"is_command": true, "intent": "request_channel_connect", "reply": "", "channels": ["canal-1"], "state": "sin_canal"}
Entrada: "conectarme al canal 1"

{"is_command": true, "intent": "request_channel_disconnect", "reply": "", "channels": [], "state": "canal-1"}
Entrada: "salir del canal"

EJEMPLOS DE CONVERSACIÓN (NO SON COMANDOS):
{"is_command": false, "intent": "conversation", "reply": "", "channels": [], "state": "canal-1"}
Entrada: "hola carlos cómo estás"

{"is_command": false, "intent": "conversation", "reply": "", "channels": [], "state": "canal-1"}
Entrada: "qué estás haciendo"

{"is_command": false, "intent": "conversation", "reply": "", "channels": [], "state": "canal-1"}
Entrada: "buenas tardes a todos"

{"is_command": false, "intent": "conversation", "reply": "", "channels": [], "state": "canal-1"}
Entrada: "carlos cómo está que está haciendo"

FORMATO DE RESPUESTA (SOLO JSON, SIN MARKDOWN):
{
  "is_command": true/false,
  "intent": "request_channel_list" | "request_channel_connect" | "request_channel_disconnect" | "conversation",
  "reply": "",
  "channels": ["canal-X"] (solo si intent=request_channel_connect),
  "state": "sin_canal" | "canal-X"
}

IMPORTANTE: 
- Responde SOLO el JSON, sin explicaciones
- Si tienes duda, marca como conversación (is_command: false)
- Solo marca comando si estás 100% seguro de las palabras clave
- SI EL USUARIO ESTA EN UN CANAL DEBES ESTAR ATENTO TAMBIEN SI EN LUGAR DE UN AUDIO, MANDA UN COMANDO, COMO POR EJEMPLO: "salir del canal-x, (x=1,2,3,4,5) o "dame la lista de canales"`
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

	// Validación adicional: si el intent no es válido, forzar conversación
	validIntents := map[string]bool{
		"request_channel_list":       true,
		"request_channel_connect":    true,
		"request_channel_disconnect": true,
		"conversation":               true,
	}

	if !validIntents[result.Intent] {
		log.Printf("WARN: Intent inválido '%s', forzando conversación", result.Intent)
		result.IsCommand = false
		result.Intent = "conversation"
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
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") {
				inCodeBlock = !inCodeBlock
				continue
			}
			if inCodeBlock && trimmed != "" {
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
	sb.WriteString("Analiza este texto:\n")
	sb.WriteString("\"")
	sb.WriteString(transcript)
	sb.WriteString("\"\n\n")

	sb.WriteString("Estado actual: ")
	sb.WriteString(currentState)
	sb.WriteString("\n")

	if pendingChannel != "" {
		sb.WriteString("Canal pendiente: ")
		sb.WriteString(pendingChannel)
		sb.WriteString("\n")
	}

	if len(channels) > 0 {
		sb.WriteString("Canales disponibles: ")
		sb.WriteString(strings.Join(channels, ", "))
		sb.WriteString("\n")
	}

	sb.WriteString("\nRecuerda: Solo marca como comando si contiene palabras clave EXACTAS. En caso de duda, marca como conversación.")
	return sb.String()
}
