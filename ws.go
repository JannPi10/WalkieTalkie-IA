package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Client struct {
	conn     *websocket.Conn
	canal    string
	username string
}

var canales = make(map[string][]*Client)
var mutex = &sync.Mutex{}
var isTalking = make(map[string]*Client) // canal → cliente que está hablando
var usuariosPorCanal = make(map[string][]string)

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()

	// Leer primer mensaje: canal (puede ser string plano o JSON)
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Println("Error al leer canal:", err)
		return
	}

	var canal string
	var canalPayload struct {
		Canal string `json:"canal"`
	}

	if err := json.Unmarshal(msg, &canalPayload); err == nil && canalPayload.Canal != "" {
		canal = canalPayload.Canal
	} else {
		canal = string(msg)
	}

	if !canalesValidos[canal] {
		conn.WriteMessage(websocket.TextMessage, []byte("Canal inválido"))
		return
	}

	username := extractUsernameFromToken(r)
	if username == "" {
		conn.WriteMessage(websocket.TextMessage, []byte("Usuario no autenticado"))
		return
	}

	client := &Client{conn: conn, canal: canal, username: username}

	if !registerClient(client) {
		conn.WriteMessage(websocket.TextMessage, []byte("Canal lleno"))
		return
	}
	defer unregisterClient(client)

	log.Printf("🟢 %s conectado a %s\n", username, canal)
	conn.WriteJSON(map[string]string{
		"message": fmt.Sprintf("Conectado al canal %s", canal),
	})

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			log.Printf("🔴 %s salió del canal %s: %v\n", username, canal, err)
			break
		}

		msgStr := string(msg)

		if msgStr == "START" {
			mutex.Lock()
			if _, ocupado := isTalking[canal]; !ocupado {
				isTalking[canal] = client
				conn.WriteMessage(websocket.TextMessage, []byte("🗣️ Puedes hablar"))
			} else {
				conn.WriteMessage(websocket.TextMessage, []byte("🚫 Otro usuario está hablando"))
			}
			mutex.Unlock()
			continue
		}

		if msgStr == "STOP" {
			mutex.Lock()
			if isTalking[canal] == client {
				delete(isTalking, canal)
				log.Printf("🔇 %s dejó de hablar en %s\n", username, canal)
			}
			mutex.Unlock()
			continue
		}

		mutex.Lock()
		speaker := isTalking[canal]
		mutex.Unlock()

		if speaker != client {
			conn.WriteMessage(websocket.TextMessage, []byte("🚫 No tienes permiso para hablar"))
			continue
		}

		fmt.Printf("📥 [%s] %s envió audio (%d bytes)\n", canal, username, len(msg))
		broadcast(client, msgType, msg)
	}
}

func registerClient(client *Client) bool {
	mutex.Lock()
	defer mutex.Unlock()

	if len(canales[client.canal]) >= maxUsuariosPorCanal {
		return false
	}

	canales[client.canal] = append(canales[client.canal], client)
	usuariosPorCanal[client.canal] = append(usuariosPorCanal[client.canal], client.username)
	return true
}

func unregisterClient(client *Client) {
	mutex.Lock()
	defer mutex.Unlock()

	var nueva []*Client
	for _, c := range canales[client.canal] {
		if c != client {
			nueva = append(nueva, c)
		}
	}
	canales[client.canal] = nueva

	var nuevosUsuarios []string
	for _, u := range usuariosPorCanal[client.canal] {
		if u != client.username {
			nuevosUsuarios = append(nuevosUsuarios, u)
		}
	}
	usuariosPorCanal[client.canal] = nuevosUsuarios

	if isTalking[client.canal] == client {
		delete(isTalking, client.canal)
	}
}

func broadcast(sender *Client, msgType int, msg []byte) {
	mutex.Lock()
	defer mutex.Unlock()

	for _, c := range canales[sender.canal] {
		if c.conn != sender.conn {
			err := c.conn.WriteMessage(msgType, msg)
			if err != nil {
				log.Println("Error al reenviar:", err)
				err := c.conn.Close()
				if err != nil {
					return
				}
			}
		}
	}
}

func listChannelsHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []string{"canal-1", "canal-2", "canal-3", "canal-4", "canal-5"})
}

func channelUsersHandler(w http.ResponseWriter, r *http.Request) {
	canal := r.URL.Query().Get("canal")
	if canal == "" || !canalesValidos[canal] {
		writeJSONError(w, http.StatusBadRequest, "Canal inválido")
		return
	}
	mutex.Lock()
	defer mutex.Unlock()
	writeJSON(w, http.StatusOK, usuariosPorCanal[canal])
}
