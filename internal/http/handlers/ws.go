package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"github.com/gorilla/websocket"
)

type wsClient struct {
	conn    *websocket.Conn
	userID  uint
	channel string
	mu      sync.Mutex
}

var (
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	registry = struct {
		sync.RWMutex
		byUser    map[uint]*wsClient
		byChannel map[string]map[uint]*wsClient
	}{
		byUser:    make(map[uint]*wsClient),
		byChannel: make(map[string]map[uint]*wsClient),
	}
)

func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	var client *wsClient
	defer func() {
		if client != nil {
			removeClient(client)
		}
		conn.Close()
	}()

	_, raw, err := conn.ReadMessage()
	if err != nil {
		log.Printf("ws handshake read: %v", err)
		return
	}

	var handshake struct {
		UserID  uint   `json:"userId"`
		Channel string `json:"channel"`
	}
	if err := json.Unmarshal(raw, &handshake); err != nil || handshake.UserID == 0 {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Handshake inválido"))
		return
	}

	var user models.User
	if err := config.DB.Preload("CurrentChannel").First(&user, handshake.UserID).Error; err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Usuario no encontrado"))
		return
	}

	channel := handshake.Channel
	if channel == "" && user.CurrentChannel != nil {
		channel = user.CurrentChannel.Code
	}

	client = &wsClient{conn: conn, userID: user.ID, channel: channel}
	registerClient(client)

	log.Printf("Cliente WebSocket conectado: usuario=%d, canal=%s", user.ID, channel)

	_ = conn.WriteJSON(map[string]string{
		"message": "Conexión establecida",
		"channel": channel,
	})

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			log.Printf("ws closed user=%d: %v", client.userID, err)
			return
		}
	}
}

func registerClient(c *wsClient) {
	registry.Lock()
	defer registry.Unlock()

	// Remover cliente anterior si existe
	if oldClient, exists := registry.byUser[c.userID]; exists {
		removeClientUnsafe(oldClient)
	}

	registry.byUser[c.userID] = c
	if c.channel != "" {
		if registry.byChannel[c.channel] == nil {
			registry.byChannel[c.channel] = make(map[uint]*wsClient)
		}
		registry.byChannel[c.channel][c.userID] = c
	}

	log.Printf("Cliente registrado: usuario=%d, canal=%s", c.userID, c.channel)
}

func removeClient(c *wsClient) {
	registry.Lock()
	defer registry.Unlock()
	removeClientUnsafe(c)
}

func removeClientUnsafe(c *wsClient) {
	delete(registry.byUser, c.userID)
	if c.channel != "" && registry.byChannel[c.channel] != nil {
		delete(registry.byChannel[c.channel], c.userID)
		if len(registry.byChannel[c.channel]) == 0 {
			delete(registry.byChannel, c.channel)
		}
	}
	log.Printf("Cliente removido: usuario=%d, canal=%s", c.userID, c.channel)
}

func moveClientToChannel(userID uint, newChannel string) {
	registry.Lock()
	defer registry.Unlock()

	client, ok := registry.byUser[userID]
	if !ok {
		log.Printf("Cliente no encontrado para mover: usuario=%d", userID)
		return
	}

	// Remover del canal anterior
	if client.channel != "" && registry.byChannel[client.channel] != nil {
		delete(registry.byChannel[client.channel], userID)
		if len(registry.byChannel[client.channel]) == 0 {
			delete(registry.byChannel, client.channel)
		}
	}

	// Agregar al nuevo canal
	client.channel = newChannel
	if newChannel != "" {
		if registry.byChannel[newChannel] == nil {
			registry.byChannel[newChannel] = make(map[uint]*wsClient)
		}
		registry.byChannel[newChannel][userID] = client
	}

	log.Printf("Cliente movido: usuario=%d, nuevo_canal=%s", userID, newChannel)

	// Notificar al cliente del cambio
	client.mu.Lock()
	defer client.mu.Unlock()
	_ = client.conn.WriteJSON(map[string]string{
		"type":    "channel_changed",
		"channel": newChannel,
	})
}

func startTransmission(channel string, speakerID uint) {
	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	if len(clients) == 0 {
		log.Printf("No hay clientes en canal %s para iniciar transmisión", channel)
		return
	}

	log.Printf("Iniciando transmisión en canal %s, hablante=%d", channel, speakerID)

	for id, c := range clients {
		signal := "STOP"
		if id == speakerID {
			signal = "START"
		}

		c.mu.Lock()
		err := c.conn.WriteJSON(map[string]string{
			"type":   "transmission",
			"signal": signal,
		})
		c.mu.Unlock()

		if err != nil {
			log.Printf("Error enviando señal %s a usuario %d: %v", signal, id, err)
		}
	}
}

func stopTransmission(channel string, speakerID uint) {
	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	if len(clients) == 0 {
		log.Printf("No hay clientes en canal %s para detener transmisión", channel)
		return
	}

	log.Printf("Deteniendo transmisión en canal %s, hablante=%d", channel, speakerID)

	for id, c := range clients {
		signal := "START"
		if id == speakerID {
			signal = "STOP"
		}

		c.mu.Lock()
		err := c.conn.WriteJSON(map[string]string{
			"type":   "transmission",
			"signal": signal,
		})
		c.mu.Unlock()

		if err != nil {
			log.Printf("Error enviando señal %s a usuario %d: %v", signal, id, err)
		}
	}
}

func broadcastAudio(channel string, senderID uint, audio []byte) {
	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	if len(clients) == 0 {
		log.Printf("No hay clientes en canal %s para broadcast de audio", channel)
		return
	}

	log.Printf("Enviando audio en canal %s desde usuario %d a %d clientes", channel, senderID, len(clients)-1)

	for id, c := range clients {
		if id == senderID {
			continue // No enviar audio al remitente
		}

		c.mu.Lock()
		err := c.conn.WriteMessage(websocket.BinaryMessage, audio)
		c.mu.Unlock()

		if err != nil {
			log.Printf("Error enviando audio a usuario %d en canal %s: %v", id, channel, err)
		}
	}
}

// GetChannelUsers obtiene los usuarios conectados via WebSocket en un canal
func GetChannelUsers(channel string) []uint {
	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	users := make([]uint, 0, len(clients))
	for userID := range clients {
		users = append(users, userID)
	}
	return users
}
