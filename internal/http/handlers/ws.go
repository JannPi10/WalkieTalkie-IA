package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"walkie-backend/pkg/deepseek"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Client struct {
	conn        *websocket.Conn
	channel     string
	userID      uint
	displayName string
}

const wakeWord = "gopebot"

var (
	mtx             = &sync.RWMutex{}
	channelConns    = make(map[string][]*Client)
	channelSpeakers = make(map[string]*Client)
	usersByChannel  = make(map[string][]string)

	deepseekOnce sync.Once
	dsClient     *deepseek.Client
	dsInitErr    error
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
		conn:        conn,
		channel:     channelName,
		userID:      uint(time.Now().UnixNano()),
		displayName: fmt.Sprintf("user-%d", time.Now().UnixNano()%10000),
	}
	if !registerClient(client, PublicMaxUsers) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Channel is full"))
		return
	}
	defer unregisterClient(client)

	log.Printf("%s connected to %s\n", client.displayName, channelName)
	_ = conn.WriteJSON(map[string]string{
		"message": fmt.Sprintf("Connected to channel %s", channelName),
		"user":    client.displayName,
	})

	ds, err := ensureDeepSeekClient()
	if err != nil {
		log.Println("ws: DeepSeek not available:", err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte("AI integration not available"))
		return
	}

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("%s left %s: %v\n", client.displayName, channelName, err)
			break
		}

		command := strings.TrimSpace(string(msg))
		if command == "" {
			continue
		}
		if isControlMessage(strings.ToUpper(command), client, channelName) {
			continue
		}
		if !strings.Contains(strings.ToLower(command), wakeWord) {
			continue
		}

		cleaned := stripWakeWord(command, wakeWord)
		res, err := ds.ProcessCommand(r.Context(), cleaned, publicChannels)
		if err != nil {
			log.Println("ws: DeepSeek error:", err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte("Error processing command with AI"))
			continue
		}
		if res.Intent == "list_channels" && len(res.Channels) == 0 && len(publicChannels) > 0 {
			res.Channels = append([]string(nil), publicChannels...)
		}

		response := map[string]any{
			"reply":  res.Reply,
			"intent": res.Intent,
		}
		if len(res.Channels) > 0 {
			response["channels"] = res.Channels
		}
		if err := conn.WriteJSON(response); err != nil {
			log.Println("ws: error sending response:", err)
			break
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
	return append([]string(nil), usersByChannel[channel]...)
}

func ensureDeepSeekClient() (*deepseek.Client, error) {
	deepseekOnce.Do(func() {
		dsClient, dsInitErr = deepseek.NewClient()
	})
	return dsClient, dsInitErr
}

func stripWakeWord(input, wake string) string {
	lower := strings.ToLower(input)
	idx := strings.Index(lower, wake)
	if idx == -1 {
		return strings.TrimSpace(input)
	}
	return strings.TrimSpace(input[idx+len(wake):])
}

func isControlMessage(cmd string, client *Client, channel string) bool {
	switch cmd {
	case "START":
		mtx.Lock()
		defer mtx.Unlock()
		if _, busy := channelSpeakers[channel]; !busy {
			channelSpeakers[channel] = client
			_ = client.conn.WriteMessage(websocket.TextMessage, []byte("Puedes hablar"))
		} else {
			_ = client.conn.WriteMessage(websocket.TextMessage, []byte("Otro usuario está hablando"))
		}
		return true
	case "STOP":
		mtx.Lock()
		defer mtx.Unlock()
		if channelSpeakers[channel] == client {
			delete(channelSpeakers, channel)
		}
		return true
	default:
		return false
	}
}
