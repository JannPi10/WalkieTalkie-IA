package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxAudioSize   = 10 * 1024 * 1024
	pingInterval   = 30 * time.Second
	pongWait       = 60 * time.Second
	writeWait      = 10 * time.Second
	maxMessageSize = 15 * 1024 * 1024
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
		CheckOrigin:     checkWSOrigin,
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

	allowedOriginsOnce sync.Once
	allowedWSOrigins   []string
)

func checkWSOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	for _, allowed := range getAllowedWSOrigins() {
		if origin == allowed {
			return true
		}
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		return false
	}

	if origin == "http://"+host || origin == "https://"+host {
		return true
	}

	log.Printf("ws origen bloqueado: origin=%s host=%s", origin, host)
	return false
}

func getAllowedWSOrigins() []string {
	allowedOriginsOnce.Do(func() {
		raw := os.Getenv("ALLOWED_WS_ORIGINS")
		if raw == "" {
			allowedWSOrigins = []string{}
			return
		}

		parts := strings.Split(raw, ",")
		allowedWSOrigins = make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				allowedWSOrigins = append(allowedWSOrigins, trimmed)
			}
		}
	})
	return allowedWSOrigins
}

func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

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

	_, raw, err := conn.ReadMessage()
	if err != nil {
		log.Printf("ws handshake read: %v", err)
		return
	}

	var handshake struct {
		UserID  uint   `json:"userId"`
		Channel string `json:"channel"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(raw, &handshake); err != nil || handshake.UserID == 0 || strings.TrimSpace(handshake.Token) == "" {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Handshake inválido"))
		return
	}

	user, err := findUserByToken(handshake.Token)
	if err != nil || user.ID != handshake.UserID {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Sesión no autorizada"))
		return
	}
	refreshUserActivity(user.ID)

	channel := strings.TrimSpace(handshake.Channel)
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

	go client.writePump()
	client.readPump()
}

func registerClient(c *wsClient) {
	registry.Lock()
	defer registry.Unlock()

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

	if client.channel != "" && registry.byChannel[client.channel] != nil {
		delete(registry.byChannel[client.channel], userID)
		if len(registry.byChannel[client.channel]) == 0 {
			delete(registry.byChannel, client.channel)
		}
	}

	if newChannel == "" {
		delete(registry.byUser, userID)
		client.channel = ""
		notifyChannelChange(client, "")
		closeWebSocket(client)
		log.Printf("Cliente desconectado: usuario=%d", userID)
		return
	}

	client.channel = newChannel
	if registry.byChannel[newChannel] == nil {
		registry.byChannel[newChannel] = make(map[uint]*wsClient)
	}
	registry.byChannel[newChannel][userID] = client

	log.Printf("Cliente movido: usuario=%d, nuevo_canal=%s", userID, newChannel)
	notifyChannelChange(client, newChannel)
}

func notifyChannelChange(c *wsClient, channel string) {
	if c == nil || c.conn == nil {
		return
	}

	payload := map[string]string{
		"type":    "channel_changed",
		"channel": channel,
	}

	c.mu.Lock()
	err := c.conn.WriteJSON(payload)
	c.mu.Unlock()

	if err != nil {
		log.Printf("Error notificando al usuario %d del cambio de canal: %v", c.userID, err)
	}
}

func closeWebSocket(c *wsClient) {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.Close()
}

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
			if _, err := w.Write(message); err != nil {
				return
			}

			n := len(c.send)
			for i := 0; i < n; i++ {
				if _, err := w.Write(<-c.send); err != nil {
					return
				}
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
		if id == speakerID {
			message["signal"] = "START"
		} else {
			message["signal"] = "STOP"
		}

		msgBytes, _ := json.Marshal(message)
		if c.conn != nil {
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.TextMessage, msgBytes)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error enviando señal START a usuario %d: %v", id, err)
			}
			continue
		}

		if c.send != nil {
			select {
			case c.send <- msgBytes:
			default:
			}
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
		"signal": "STOP",
	}

	msgBytes, _ := json.Marshal(message)

	for id, c := range clients {
		if c.conn != nil {
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.TextMessage, msgBytes)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error enviando señal STOP a usuario %d: %v", id, err)
			}
			continue
		}

		if c.send != nil {
			select {
			case c.send <- msgBytes:
			default:
			}
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

	log.Printf("Broadcasting audio en canal %s desde usuario %d a %d clientes", channel, senderID, len(clients))

	for id, c := range clients {
		if c.conn != nil {
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.BinaryMessage, audio)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error enviando audio a usuario %d en canal %s: %v", id, channel, err)
			}
			continue
		}

		if c.send != nil {
			select {
			case c.send <- audio:
			default:
			}
		}
	}
}
