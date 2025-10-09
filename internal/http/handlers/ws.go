package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/response"
	"walkie-backend/pkg/security"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type wsJoinPayload struct {
	Channel string `json:"channel"`
	PIN     string `json:"pin,omitempty"`
}

type Client struct {
	conn        *websocket.Conn
	channel     string
	userID      uint
	displayName string
}

var (
	mtx             = &sync.RWMutex{}
	channelConns    = make(map[string][]*Client)
	channelSpeakers = make(map[string]*Client)
	usersByChannel  = make(map[string][]string) // para /channel-users
)

func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	uid, _, err := security.MustUserIDEmail(r)
	if err != nil {
		response.WriteErr(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	var user models.User
	if err := config.DB.First(&user, uid).Error; err != nil {
		response.WriteErr(w, http.StatusUnauthorized, "Usuario no encontrado")
		return
	}
	displayName := strings.TrimSpace(user.FirstName + " " + user.LastName)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}

	// soporta string plano o JSON { "channel": "...", "pin": "...." }
	payload := wsJoinPayload{}
	channelName := string(raw)
	if json.Unmarshal(raw, &payload) == nil && payload.Channel != "" {
		channelName = payload.Channel
	}

	// público vs privado
	isPublic := false
	for _, p := range publicChannels {
		if strings.EqualFold(p, channelName) {
			isPublic = true
			break
		}
	}

	maxUsers := PublicMaxUsers
	if !isPublic {
		var ch models.Channel
		if err := config.DB.Where("name = ?", channelName).First(&ch).Error; err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte("Canal inválido"))
			return
		}
		if !ch.IsPrivate {
			// si el canal existe y es público en DB (hoy no creamos públicos en DB), lo tratamos como público
			isPublic = false
		}
		maxUsers = ch.MaxUsers
		if ch.IsPrivate {
			if ch.PinHash == nil || payload.PIN == "" || !security.CheckPIN(*ch.PinHash, payload.PIN) {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("PIN incorrecto"))
				return
			}
		}
	}

	client := &Client{conn: conn, channel: channelName, userID: uid, displayName: displayName}
	if !registerClient(client, maxUsers) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("Canal lleno"))
		return
	}
	defer unregisterClient(client)

	log.Printf("%s conectado a %s\n", displayName, channelName)
	_ = conn.WriteJSON(map[string]string{"message": fmt.Sprintf("Conectado al canal %s", channelName)})

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("%s salió de %s: %v\n", displayName, channelName, err)
			break
		}
		switch string(msg) {
		case "START":
			mtx.Lock()
			if _, busy := channelSpeakers[channelName]; !busy {
				channelSpeakers[channelName] = client
				_ = conn.WriteMessage(websocket.TextMessage, []byte("Puedes hablar"))
			} else {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("Otro usuario está hablando"))
			}
			mtx.Unlock()
			continue
		case "STOP":
			mtx.Lock()
			if channelSpeakers[channelName] == client {
				delete(channelSpeakers, channelName)
			}
			mtx.Unlock()
			continue
		}

		mtx.RLock()
		speaker := channelSpeakers[channelName]
		mtx.RUnlock()
		if speaker != client {
			_ = conn.WriteMessage(websocket.TextMessage, []byte("No tienes permiso para hablar"))
			continue
		}
		broadcast(client, msgType, msg)
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
	for _, x := range channelConns[c.channel] {
		if x != c {
			remain = append(remain, x)
		}
	}
	channelConns[c.channel] = remain

	var names []string
	for _, n := range usersByChannel[c.channel] {
		if n != c.displayName {
			names = append(names, n)
		}
	}
	usersByChannel[c.channel] = names

	if channelSpeakers[c.channel] == c {
		delete(channelSpeakers, c.channel)
	}
}

func broadcast(sender *Client, msgType int, msg []byte) {
	mtx.RLock()
	defer mtx.RUnlock()
	for _, c := range channelConns[sender.channel] {
		if c.conn != sender.conn {
			_ = c.conn.WriteMessage(msgType, msg)
		}
	}
}

// usado por /channel-users
func GetUsersInChannel(channel string) []string {
	mtx.RLock()
	defer mtx.RUnlock()
	return append([]string(nil), usersByChannel[channel]...)
}
