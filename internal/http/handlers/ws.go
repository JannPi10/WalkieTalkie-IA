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

	var (
		client *wsClient
	)
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

	registry.byUser[c.userID] = c
	if c.channel != "" {
		if registry.byChannel[c.channel] == nil {
			registry.byChannel[c.channel] = make(map[uint]*wsClient)
		}
		registry.byChannel[c.channel][c.userID] = c
	}
}

func removeClient(c *wsClient) {
	registry.Lock()
	defer registry.Unlock()

	delete(registry.byUser, c.userID)
	if c.channel != "" && registry.byChannel[c.channel] != nil {
		delete(registry.byChannel[c.channel], c.userID)
		if len(registry.byChannel[c.channel]) == 0 {
			delete(registry.byChannel, c.channel)
		}
	}
}

func moveClientToChannel(userID uint, newChannel string) {
	registry.Lock()
	defer registry.Unlock()

	client, ok := registry.byUser[userID]
	if !ok {
		return
	}

	if client.channel != "" && registry.byChannel[client.channel] != nil {
		delete(registry.byChannel[client.channel], userID)
		if len(registry.byChannel[client.channel]) == 0 {
			delete(registry.byChannel, client.channel)
		}
	}

	client.channel = newChannel
	if newChannel != "" {
		if registry.byChannel[newChannel] == nil {
			registry.byChannel[newChannel] = make(map[uint]*wsClient)
		}
		registry.byChannel[newChannel][userID] = client
	}
}

func startTransmission(channel string, speakerID uint) {
	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	for id, c := range clients {
		msg := "STOP"
		if id == speakerID {
			msg = "START"
		}
		_ = c.conn.WriteMessage(websocket.TextMessage, []byte(msg))
	}
}

func stopTransmission(channel string, speakerID uint) {
	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	for id, c := range clients {
		msg := "START"
		if id == speakerID {
			msg = "STOP"
		}
		_ = c.conn.WriteMessage(websocket.TextMessage, []byte(msg))
	}
}

func broadcastAudio(channel string, senderID uint, audio []byte) {
	registry.RLock()
	defer registry.RUnlock()

	for id, c := range registry.byChannel[channel] {
		if id == senderID {
			continue
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, audio); err != nil {
			log.Printf("ws audio send channel=%s user=%d: %v", channel, id, err)
		}
	}
}
