package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"walkie-backend/pkg/deepseek"
	"walkie-backend/pkg/stt"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Client struct {
	conn              *websocket.Conn
	channel           string
	userID            uint
	displayName       string
	conversationState string
	pendingChannel    string
	isTransmitting    bool
}

var (
	mtx             = &sync.RWMutex{}
	channelConns    = make(map[string][]*Client)
	channelSpeakers = make(map[string]*Client)
	usersByChannel  = make(map[string][]string)

	deepseekOnce sync.Once
	dsClient     *deepseek.Client
	dsInitErr    error

	sttOnce    sync.Once
	sttClient  *stt.Client
	sttInitErr error
)

func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("ws: upgrade error:", err)
		return
	}
	defer conn.Close()

	_, raw, err := conn.ReadMessage()
	if err != nil {
		log.Println("ws: read error:", err)
		return
	}

	var payload struct {
		Channel string `json:"channel"`
	}
	channelName := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &payload) == nil && payload.Channel != "" {
		channelName = strings.TrimSpace(payload.Channel)
	}
	if channelName == "" {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Channel name is required"))
		return
	}

	client := &Client{
		conn:              conn,
		channel:           channelName,
		userID:            uint(time.Now().UnixNano()),
		displayName:       fmt.Sprintf("user-%d", time.Now().UnixNano()%10000),
		conversationState: "normal",
		isTransmitting:    false,
	}

	if !registerClient(client, PublicMaxUsers) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Channel is full"))
		return
	}
	defer unregisterClient(client)

	log.Printf("%s connected to %s\n", client.displayName, channelName)
	_ = conn.WriteJSON(map[string]string{
		"message": fmt.Sprintf("Conectado al canal %s", channelName),
		"user":    client.displayName,
	})

	ds, err := ensureDeepSeekClient()
	if err != nil {
		log.Println("ws: DeepSeek not available:", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte("AI integration not available"))
		return
	}

	stt, err := ensureSTTClient()
	if err != nil {
		log.Println("ws: STT not available:", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Speech recognition not available"))
		return
	}

	for {
		messageType, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("%s left %s: %v\n", client.displayName, channelName, err)
			break
		}

		// Manejar comandos de control directo
		if messageType == websocket.TextMessage {
			command := strings.TrimSpace(string(msg))
			if isControlMessage(strings.ToUpper(command), client) {
				continue
			}
		}

		// Manejar audio binario
		if messageType == websocket.BinaryMessage {
			handleAudioMessage(client, msg, ds, stt, r)
			continue
		}
	}
}

func handleAudioMessage(client *Client, audioData []byte, ds *deepseek.Client, sttClient *stt.Client, r *http.Request) {
	// Verificar si es audio humano
	if !sttClient.IsHumanSpeech(audioData) {
		log.Printf("Audio no humano detectado de %s, ignorando", client.displayName)
		return
	}

	// Convertir audio a texto
	transcript, err := sttClient.TranscribeAudio(r.Context(), audioData)
	if err != nil {
		log.Printf("Error transcribiendo audio de %s: %v", client.displayName, err)
		return
	}

	if transcript == "" {
		log.Printf("Transcripción vacía de %s", client.displayName)
		return
	}

	log.Printf("Transcripción de %s: %s", client.displayName, transcript)

	// Analizar si es comando o conversación
	result, err := ds.AnalyzeTranscript(r.Context(), transcript, publicChannels, client.conversationState, client.pendingChannel)
	if err != nil {
		log.Printf("Error analizando transcripción: %v", err)
		result = deepseek.CommandResult{
			IsCommand: false,
			Intent:    "unknown",
			Reply:     "Lo siento, no pude procesar tu comando. ¿Puedes repetirlo?",
			State:     "normal",
		}
	}

	// Actualizar estado del cliente
	client.conversationState = result.State
	client.pendingChannel = result.PendingChannel

	if result.IsCommand {
		// Es un comando - procesar y responder
		handleCommand(client, result)
	} else {
		// Es conversación - relay audio a otros usuarios del canal
		if client.channel != "" {
			startTransmission(client)
			relayAudioToChannel(client, audioData)
			// El STOP se enviará cuando termine la transmisión o después de un timeout
			go func() {
				time.Sleep(2 * time.Second) // Timeout para finalizar transmisión
				stopTransmission(client)
			}()
		}
	}
}

func handleCommand(client *Client, result deepseek.CommandResult) {
	switch result.Intent {
	case "confirm_channel_list":
		if len(result.Channels) == 0 && len(publicChannels) > 0 {
			result.Channels = append([]string(nil), publicChannels...)
		}
		result.Reply = fmt.Sprintf("Aquí tienes los canales disponibles: %s", strings.Join(result.Channels, ", "))

	case "confirm_channel_connect":
		if client.pendingChannel != "" && isValidChannel(client.pendingChannel) {
			oldChannel := client.channel
			switchClientChannel(client, client.pendingChannel)
			result.Reply = fmt.Sprintf("Te he conectado al %s. ¡Bienvenido!", client.pendingChannel)
			log.Printf("%s cambió de %s a %s", client.displayName, oldChannel, client.pendingChannel)
			client.pendingChannel = ""
		} else {
			result.Reply = "No hay canal pendiente para conectar."
		}

	case "deny_action":
		result.Reply = "Entendido, cancelando la acción."
		client.pendingChannel = ""

	case "request_channel_connect":
		// Extraer canal del comando
		if len(result.Channels) > 0 {
			client.pendingChannel = result.Channels[0]
			result.Reply = fmt.Sprintf("¿Deseas conectarte al %s?", client.pendingChannel)
		} else {
			result.Reply = "¿A qué canal te quieres conectar? Dime canal-1, canal-2, canal-3, canal-4 o canal-5."
		}
	}

	// Enviar respuesta JSON al cliente
	response := map[string]any{
		"reply":  result.Reply,
		"intent": result.Intent,
		"state":  result.State,
	}
	if len(result.Channels) > 0 {
		response["channels"] = result.Channels
	}

	if err := client.conn.WriteJSON(response); err != nil {
		log.Printf("Error enviando respuesta a %s: %v", client.displayName, err)
	}
}

func startTransmission(client *Client) {
	mtx.Lock()
	defer mtx.Unlock()

	// Marcar cliente como transmitiendo
	client.isTransmitting = true
	channelSpeakers[client.channel] = client

	// Enviar START al cliente que habla
	_ = client.conn.WriteMessage(websocket.TextMessage, []byte("START"))

	// Enviar STOP a todos los demás en el canal
	for _, other := range channelConns[client.channel] {
		if other != client {
			_ = other.conn.WriteMessage(websocket.TextMessage, []byte("STOP"))
		}
	}
}

func stopTransmission(client *Client) {
	mtx.Lock()
	defer mtx.Unlock()

	if !client.isTransmitting {
		return
	}

	client.isTransmitting = false
	if channelSpeakers[client.channel] == client {
		delete(channelSpeakers, client.channel)
	}

	// Enviar STOP al cliente que terminó de hablar
	_ = client.conn.WriteMessage(websocket.TextMessage, []byte("STOP"))
}

func relayAudioToChannel(sender *Client, audioData []byte) {
	mtx.RLock()
	defer mtx.RUnlock()

	// Enviar audio a todos los demás usuarios del canal
	for _, client := range channelConns[sender.channel] {
		if client != sender {
			if err := client.conn.WriteMessage(websocket.BinaryMessage, audioData); err != nil {
				log.Printf("Error enviando audio a %s: %v", client.displayName, err)
			}
		}
	}
}

func registerClient(c *Client, maxUsers int) bool {
	mtx.Lock()
	defer mtx.Unlock()
	if len(channelConns[c.channel]) >= maxUsers {
		return false
	}
	channelConns[c.channel] = append(channelConns[c.channel], c)
	usersByChannel[c.channel] = append(usersByChannel[c.channel], c.displayName)
	return true
}

func unregisterClient(c *Client) {
	mtx.Lock()
	defer mtx.Unlock()

	var remain []*Client
	for _, other := range channelConns[c.channel] {
		if other != c {
			remain = append(remain, other)
		}
	}
	channelConns[c.channel] = remain

	var names []string
	for _, name := range usersByChannel[c.channel] {
		if name != c.displayName {
			names = append(names, name)
		}
	}
	usersByChannel[c.channel] = names

	if channelSpeakers[c.channel] == c {
		delete(channelSpeakers, c.channel)
	}
}

func GetUsersInChannel(channel string) []string {
	mtx.RLock()
	defer mtx.RUnlock()
	users := usersByChannel[channel]
	if len(users) == 0 {
		return []string{}
	}
	return append([]string(nil), users...)
}

func ensureDeepSeekClient() (*deepseek.Client, error) {
	deepseekOnce.Do(func() {
		dsClient, dsInitErr = deepseek.NewClient()
	})
	return dsClient, dsInitErr
}

func ensureSTTClient() (*stt.Client, error) {
	sttOnce.Do(func() {
		sttClient, sttInitErr = stt.NewClient()
	})
	return sttClient, sttInitErr
}

func isValidChannel(channel string) bool {
	validChannels := []string{"canal-1", "canal-2", "canal-3", "canal-4", "canal-5"}
	for _, valid := range validChannels {
		if channel == valid {
			return true
		}
	}
	return false
}

func switchClientChannel(client *Client, newChannel string) {
	mtx.Lock()
	defer mtx.Unlock()

	// Remover del canal actual
	oldChannel := client.channel
	var remain []*Client
	for _, other := range channelConns[oldChannel] {
		if other != client {
			remain = append(remain, other)
		}
	}
	channelConns[oldChannel] = remain

	var names []string
	for _, name := range usersByChannel[oldChannel] {
		if name != client.displayName {
			names = append(names, name)
		}
	}
	usersByChannel[oldChannel] = names

	// Añadir al nuevo canal
	client.channel = newChannel
	channelConns[newChannel] = append(channelConns[newChannel], client)
	usersByChannel[newChannel] = append(usersByChannel[newChannel], client.displayName)
}

func isControlMessage(cmd string, client *Client) bool {
	switch cmd {
	case "START":
		startTransmission(client)
		return true
	case "STOP":
		stopTransmission(client)
		return true
	default:
		return false
	}
}
