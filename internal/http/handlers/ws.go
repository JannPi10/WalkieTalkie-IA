package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"github.com/gorilla/websocket"
)

const (
	// Límites de seguridad
	maxAudioSize   = 10 * 1024 * 1024 // 10 MB
	pingInterval   = 30 * time.Second
	pongWait       = 60 * time.Second
	writeWait      = 10 * time.Second
	maxMessageSize = 15 * 1024 * 1024 // 15 MB
)

type wsClient struct {
	conn    *websocket.Conn
	userID  uint
	channel string
	mu      sync.Mutex
	send    chan []byte
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

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

	// Configurar límites
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	var client *wsClient
	defer func() {
		if client != nil {
			removeClient(client)
			close(client.send)
		}
		conn.Close()
	}()

	// Leer handshake
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

	// Validar usuario
	var user models.User
	if err := config.DB.Preload("CurrentChannel").First(&user, handshake.UserID).Error; err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Usuario no encontrado"))
		return
	}

	channel := handshake.Channel
	if channel == "" && user.CurrentChannel != nil {
		channel = user.CurrentChannel.Code
	}

	client = &wsClient{
		conn:    conn,
		userID:  user.ID,
		channel: channel,
		send:    make(chan []byte, 256),
	}
	registerClient(client)

	log.Printf("Cliente WebSocket conectado: usuario=%d, canal=%s", user.ID, channel)

	_ = conn.WriteJSON(map[string]string{
		"message": "Conexión establecida",
		"channel": channel,
	})

	// Iniciar goroutines para lectura y escritura
	go client.writePump()
	go client.readPump()
}

// readPump lee mensajes del WebSocket
func (c *wsClient) readPump() {
	defer func() {
		removeClient(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("ws error user=%d: %v", c.userID, err)
			}
			break
		}
	}
}

// writePump envía mensajes al WebSocket
func (c *wsClient) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Agregar mensajes en cola
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
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
		log.Printf("No hay clientes WebSocket en canal %s para iniciar transmisión", channel)
		return
	}

	log.Printf("Iniciando transmisión en canal %s, hablante=%d", channel, speakerID)

	message := map[string]interface{}{
		"type":   "transmission",
		"from":   speakerID,
		"action": "start",
	}

	for id, c := range clients {
		// STOP para todos excepto el que habla
		if id == speakerID {
			message["signal"] = "START" // El que habla puede seguir
		} else {
			message["signal"] = "STOP" // Los demás deben callar
		}

		msgBytes, _ := json.Marshal(message)
		c.mu.Lock()
		err := c.conn.WriteMessage(websocket.TextMessage, msgBytes)
		c.mu.Unlock()

		if err != nil {
			log.Printf("Error enviando señal START a usuario %d: %v", id, err)
		}
	}
}

func stopTransmission(channel string, speakerID uint) {
	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	if len(clients) == 0 {
		log.Printf("No hay clientes WebSocket en canal %s para detener transmisión", channel)
		return
	}

	log.Printf("Deteniendo transmisión en canal %s, hablante=%d", channel, speakerID)

	message := map[string]interface{}{
		"type":   "transmission",
		"from":   speakerID,
		"action": "stop",
		"signal": "START",
	}

	msgBytes, _ := json.Marshal(message)

	for id, c := range clients {
		c.mu.Lock()
		err := c.conn.WriteMessage(websocket.TextMessage, msgBytes)
		c.mu.Unlock()

		if err != nil {
			log.Printf("Error enviando señal STOP a usuario %d: %v", id, err)
		}
	}
}

func broadcastAudio(channel string, senderID uint, audio []byte) {
	if len(audio) > maxAudioSize {
		log.Printf("Audio demasiado grande: %d bytes (max: %d)", len(audio), maxAudioSize)
		return
	}

	registry.RLock()
	defer registry.RUnlock()

	clients := registry.byChannel[channel]
	if len(clients) == 0 {
		log.Printf("No hay clientes WebSocket en canal %s para broadcast de audio", channel)
		return
	}

	log.Printf("Broadcasting audio en canal %s desde usuario %d a %d clientes", channel, senderID, len(clients)-1)

	for id, c := range clients {
		if id == senderID {
			continue
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
