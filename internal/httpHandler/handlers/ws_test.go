package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Channel{}, &models.ChannelMembership{}); err != nil {
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

func createTestUser(t *testing.T, db *gorm.DB, id uint, token string, channelCode string) *models.User {
	user := &models.User{
		Model:        gorm.Model{ID: id},
		DisplayName:  "testuser",
		AuthToken:    token,
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
	user := createTestUser(t, db, 1, "token-123", "testchannel")

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
		"token":   user.AuthToken,
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
	assert.Equal(t, "Conexión establecida", resp["message"])
	assert.Equal(t, "testchannel", resp["channel"])
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
	assert.Equal(t, "Handshake inválido", string(response))
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
		"token":   "bad-token",
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
	assert.Equal(t, "Sesión no autorizada", string(response))
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
	assert.Equal(t, client, registry.byUser[1])
	assert.Equal(t, client, registry.byChannel["test"][1])
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
	assert.NotContains(t, registry.byUser, uint(1))
	assert.NotContains(t, registry.byChannel["test"], uint(1))
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
	assert.Equal(t, "new", client.channel)
	assert.NotContains(t, registry.byChannel["old"], uint(1))
	assert.Equal(t, client, registry.byChannel["new"][1])
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
	assert.Equal(t, "", client.channel)
	assert.NotContains(t, registry.byChannel["old"], uint(1))
	assert.NotContains(t, registry.byUser, uint(1))
}

func TestStartTransmission(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client1 := &wsClient{userID: 1, channel: "test", send: make(chan []byte, 1)}
	client2 := &wsClient{userID: 2, channel: "test", send: make(chan []byte, 1)}

	registerClient(client1)
	registerClient(client2)

	startTransmission("test", 1)

	select {
	case msg := <-client1.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		assert.Equal(t, "START", m["signal"])
	default:
		t.Errorf("client1 did not receive message")
	}

	select {
	case msg := <-client2.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		assert.Equal(t, "STOP", m["signal"])
	default:
		t.Errorf("client2 did not receive message")
	}
}

func TestStopTransmission(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client1 := &wsClient{userID: 1, channel: "test", send: make(chan []byte, 1)}
	client2 := &wsClient{userID: 2, channel: "test", send: make(chan []byte, 1)}

	registerClient(client1)
	registerClient(client2)

	stopTransmission("test", 1)

	select {
	case msg := <-client1.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		assert.Equal(t, "STOP", m["signal"])
	default:
		t.Errorf("client1 did not receive message")
	}

	select {
	case msg := <-client2.send:
		var m map[string]string
		json.Unmarshal(msg, &m)
		assert.Equal(t, "STOP", m["signal"])
	default:
		t.Errorf("client2 did not receive message")
	}
}

func TestBroadcastAudio(t *testing.T) {
	registry.Lock()
	registry.byUser = make(map[uint]*wsClient)
	registry.byChannel = make(map[string]map[uint]*wsClient)
	registry.Unlock()

	client1 := &wsClient{userID: 1, channel: "test", send: make(chan []byte, 1)}
	client2 := &wsClient{userID: 2, channel: "test", send: make(chan []byte, 1)}

	registerClient(client1)
	registerClient(client2)

	audioData := []byte("audio data")
	broadcastAudio("test", 1, audioData)

	select {
	case received := <-client1.send:
		assert.True(t, bytes.Equal(received, audioData))
	default:
		t.Errorf("client1 did not receive audio")
	}

	select {
	case received := <-client2.send:
		assert.True(t, bytes.Equal(received, audioData))
	default:
		t.Errorf("client2 did not receive audio")
	}
}

func TestCheckWSOrigin(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins string
		originHeader   string
		hostHeader     string
		expected       bool
	}{
		{"allowed origin", "http://foo.com,http://bar.com", "http://foo.com", "my-app.com", true},
		{"not allowed origin", "http://foo.com", "http://baz.com", "my-app.com", false},
		{"empty origin is allowed", "http://foo.com", "", "my-app.com", true},
		{"host matches origin http", "http://foo.com", "http://my-app.com", "my-app.com", true},
		{"host matches origin https", "http://foo.com", "https://my-app.com", "my-app.com", true},
		{"subdomain not implicitly allowed", "http://foo.com", "http://sub.foo.com", "my-app.com", false},
		{"no allowed origins set, origin not empty", "", "http://any.com", "my-app.com", false},
		{"no allowed origins, but host matches", "", "http://my-app.com", "my-app.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset allowed origins cache for each sub-test
			allowedOriginsOnce = sync.Once{}
			t.Setenv("ALLOWED_WS_ORIGINS", tt.allowedOrigins)

			req := httptest.NewRequest("GET", "http://my-app.com/ws", nil)
			if tt.originHeader != "" {
				req.Header.Set("Origin", tt.originHeader)
			}
			if tt.hostHeader != "" {
				req.Host = tt.hostHeader
			}

			if got := checkWSOrigin(req); got != tt.expected {
				t.Errorf("checkWSOrigin() for origin '%s' and host '%s' = %v, want %v", tt.originHeader, tt.hostHeader, got, tt.expected)
			}
		})
	}
}

func TestWebSocket_ReadPump_Close(t *testing.T) {
	db := setupTestDB(t)
	user := createTestUser(t, db, 1, "token-123", "testchannel")

	s := httptest.NewServer(http.HandlerFunc(HandleWebSocket))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assert.NoError(t, err)

	// Handshake
	handshake := map[string]interface{}{"userId": user.ID, "token": user.AuthToken}
	handshakeBytes, _ := json.Marshal(handshake)
	conn.WriteMessage(websocket.TextMessage, handshakeBytes)
	conn.ReadMessage() // Read the "Conexión establecida" message

	// Check that client is registered
	registry.RLock()
	_, ok := registry.byUser[user.ID]
	registry.RUnlock()
	assert.True(t, ok, "client should be registered")

	// Close connection
	conn.Close()

	// Wait a bit for readPump to process the close and unregister the client
	assert.Eventually(t, func() bool {
		registry.RLock()
		_, ok := registry.byUser[user.ID]
		registry.RUnlock()
		return !ok
	}, 200*time.Millisecond, 20*time.Millisecond, "client should be unregistered after connection close")
}

func TestWebSocket_WritePump(t *testing.T) {
	// 1. Setup server and client
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		assert.NoError(t, err)
		defer conn.Close()

		// Just read messages to keep the connection alive
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assert.NoError(t, err)
	defer conn.Close()

	client := &wsClient{
		conn:    conn,
		userID:  1,
		send:    make(chan []byte, 2),
	}

	// 2. Run writePump in a goroutine
	go client.writePump()

	// 3. Test sending a message
	testMessage := []byte("hello")
	client.send <- testMessage

	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	msgType, msg, err := conn.ReadMessage()
	assert.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, msgType)
	assert.Equal(t, testMessage, msg)

	// 4. Test closing the send channel
	close(client.send)

	// writePump should send a CloseMessage
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, _, err = conn.ReadMessage()
	assert.Error(t, err)
	assert.True(t, websocket.IsCloseError(err, websocket.CloseNormalClosure), "expected normal close error")
}