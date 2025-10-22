package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"github.com/gorilla/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Channel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	oldDB := config.DB
	config.DB = db
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		config.DB = oldDB
	})
	return db
}

func createTestUser(t *testing.T, db *gorm.DB, id uint, channelCode string) *models.User {
	user := &models.User{
		DisplayName:  "testuser",
		AuthToken:    "token",
		IsActive:     true,
		LastActiveAt: time.Now(),
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if channelCode != "" {
		channel := &models.Channel{Code: channelCode, Name: "Test Channel", MaxUsers: 100}
		if err := db.Create(channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		user.CurrentChannelID = &channel.ID
		user.CurrentChannel = channel
		if err := db.Save(user).Error; err != nil {
			t.Fatalf("save user: %v", err)
		}
	}
	return user
}

func TestHandleWebSocket_ValidHandshake(t *testing.T) {
	db := setupTestDB(t)
	user := createTestUser(t, db, 1, "testchannel")

	s := httptest.NewServer(http.HandlerFunc(HandleWebSocket))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	handshake := map[string]interface{}{
		"userId":  user.ID,
		"channel": "testchannel",
	}
	handshakeBytes, _ := json.Marshal(handshake)
	if err := conn.WriteMessage(websocket.TextMessage, handshakeBytes); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	_, response, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var resp map[string]string
	if err := json.Unmarshal(response, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["message"] != "Conexión establecida" {
		t.Errorf("unexpected message: %s", resp["message"])
	}
}

func TestHandleWebSocket_InvalidHandshake(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(HandleWebSocket))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("invalid json")); err != nil {
		t.Fatalf("write invalid handshake: %v", err)
	}

	_, response, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(response) != "Handshake inválido" {
		t.Errorf("expected 'Handshake inválido', got %s", string(response))
	}
}

func TestHandleWebSocket_UserNotFound(t *testing.T) {
	_ = setupTestDB(t)

	s := httptest.NewServer(http.HandlerFunc(HandleWebSocket))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	handshake := map[string]interface{}{
		"userId":  999,
		"channel": "testchannel",
	}
	handshakeBytes, _ := json.Marshal(handshake)
	if err := conn.WriteMessage(websocket.TextMessage, handshakeBytes); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	_, response, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(response) != "Usuario no encontrado" {
		t.Errorf("expected 'Usuario no encontrado', got %s", string(response))
	}
}

func TestRegisterClient(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client := &wsClient{
		userID:  1,
		channel: "test",
		send:    make(chan []byte, 1),
	}

	registerClient(client)

	registry.RLock()
	defer registry.RUnlock()
	if registry.byUser[1] != client {
		t.Errorf("client not registered in byUser")
	}
	if registry.byChannel["test"][1] != client {
		t.Errorf("client not registered in byChannel")
	}
}

func TestRemoveClient(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client := &wsClient{
		userID:  1,
		channel: "test",
		send:    make(chan []byte, 1),
	}

	registerClient(client)
	removeClient(client)

	registry.RLock()
	defer registry.RUnlock()
	if _, exists := registry.byUser[1]; exists {
		t.Errorf("client still in byUser")
	}
	if _, exists := registry.byChannel["test"][1]; exists {
		t.Errorf("client still in byChannel")
	}
}

func TestMoveClientToChannel(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client := &wsClient{
		userID:  1,
		channel: "old",
		send:    make(chan []byte, 1),
	}

	registerClient(client)
	moveClientToChannel(1, "new")

	registry.RLock()
	defer registry.RUnlock()
	if client.channel != "new" {
		t.Errorf("client channel not updated")
	}
	if _, exists := registry.byChannel["old"][1]; exists {
		t.Errorf("client still in old channel")
	}
	if registry.byChannel["new"][1] != client {
		t.Errorf("client not in new channel")
	}
}

func TestMoveClientToChannel_Disconnect(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client := &wsClient{
		userID:  1,
		channel: "old",
		send:    make(chan []byte, 1),
	}

	registerClient(client)
	moveClientToChannel(1, "")

	registry.RLock()
	defer registry.RUnlock()
	if client.channel != "" {
		t.Errorf("client channel not updated to empty")
	}
	if _, exists := registry.byChannel["old"][1]; exists {
		t.Errorf("client still in old channel")
	}
	if _, exists := registry.byUser[1]; exists {
		t.Errorf("client still in byUser")
	}
}

func TestStartTransmission(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client1 := &wsClient{
		userID:  1,
		channel: "test",
		send:    make(chan []byte, 1),
	}
	client2 := &wsClient{
		userID:  2,
		channel: "test",
		send:    make(chan []byte, 1),
	}

	registerClient(client1)
	registerClient(client2)

	startTransmission("test", 1)

	select {
	case msg := <-client1.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		if m["signal"] != "START" {
			t.Errorf("client1 should get START, got %s", m["signal"])
		}
	default:
		t.Errorf("client1 did not receive message")
	}

	select {
	case msg := <-client2.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		if m["signal"] != "STOP" {
			t.Errorf("client2 should get STOP, got %s", m["signal"])
		}
	default:
		t.Errorf("client2 did not receive message")
	}
}

func TestStopTransmission(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client1 := &wsClient{
		userID:  1,
		channel: "test",
		send:    make(chan []byte, 1),
	}
	client2 := &wsClient{
		userID:  2,
		channel: "test",
		send:    make(chan []byte, 1),
	}

	registerClient(client1)
	registerClient(client2)

	stopTransmission("test", 1)

	select {
	case msg := <-client1.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		if m["signal"] != "STOP" {
			t.Errorf("client1 should get STOP, got %s", m["signal"])
		}
	default:
		t.Errorf("client1 did not receive message")
	}

	select {
	case msg := <-client2.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		if m["signal"] != "STOP" {
			t.Errorf("client2 should get STOP, got %s", m["signal"])
		}
	default:
		t.Errorf("client2 did not receive message")
	}
}

func TestBroadcastAudio(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client1 := &wsClient{
		userID:  1,
		channel: "test",
		send:    make(chan []byte, 1),
	}
	client2 := &wsClient{
		userID:  2,
		channel: "test",
		send:    make(chan []byte, 1),
	}

	registerClient(client1)
	registerClient(client2)

	audioData := []byte("audio data")
	broadcastAudio("test", 1, audioData)

	select {
	case received := <-client1.send:
		if !bytes.Equal(received, audioData) {
			t.Errorf("client1 received wrong audio")
		}
	default:
		t.Errorf("client1 did not receive audio")
	}

	select {
	case received := <-client2.send:
		if !bytes.Equal(received, audioData) {
			t.Errorf("client2 received wrong audio")
		}
	default:
		t.Errorf("client2 did not receive audio")
	}
}
