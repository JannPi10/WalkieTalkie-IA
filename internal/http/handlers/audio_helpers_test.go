// internal/http/handlers/audio_helpers_test.go
package handlers

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/services"
	"walkie-backend/pkg/qwen"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	mockMoveClientToChannel   func(uint, string)
	mockStartTransmission     func(string, uint)
	mockStopTransmission      func(string, uint)
	mockBroadcastAudio        func(string, uint, []byte)
	mockEnqueueAudio          func(uint, string, []byte, float64, []uint)
	mockDequeueAudio          func(uint) *PendingAudio
	mockGetChannelActiveUsers func(*services.UserService, string) ([]models.User, error)
)

func init() {
	mockMoveClientToChannel = moveClientToChannel
	mockStartTransmission = startTransmission
	mockStopTransmission = stopTransmission
	mockBroadcastAudio = broadcastAudio
	mockEnqueueAudio = EnqueueAudio
	mockDequeueAudio = DequeueAudio
	mockGetChannelActiveUsers = func(svc *services.UserService, code string) ([]models.User, error) {
		return svc.GetChannelActiveUsers(code)
	}
}

var helperCounter uint64

func withTestDB(t *testing.T, fn func(db *gorm.DB)) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.Channel{}, &models.ChannelMembership{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	oldDB := config.DB
	config.DB = db
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		config.DB = oldDB
	}()

	fn(db)
}

func createChannel(t *testing.T, db *gorm.DB, code string) *models.Channel {
	t.Helper()

	ch := &models.Channel{
		Code:     code,
		Name:     fmt.Sprintf("Canal %s", code),
		MaxUsers: 100,
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return ch
}

func createUser(t *testing.T, db *gorm.DB, opts ...func(*models.User)) *models.User {
	t.Helper()

	id := atomic.AddUint64(&helperCounter, 1)
	user := &models.User{
		DisplayName:  fmt.Sprintf("tester-%d", id),
		AuthToken:    fmt.Sprintf("token-%d", id),
		IsActive:     true,
		LastActiveAt: time.Now(),
	}
	for _, opt := range opts {
		opt(user)
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func cleanupAudioQueue() {
	globalAudioQueue.mu.Lock()
	defer globalAudioQueue.mu.Unlock()
	globalAudioQueue.queues = make(map[uint][]*PendingAudio)
}

func TestHandleChannelListCommand_ReturnsList(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		createChannel(t, db, "canal-1")
		createChannel(t, db, "canal-2")
		createChannel(t, db, "canal-3")

		resp, err := handleChannelListCommand(svc)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		expected := "Canales disponibles: 1, 2, y 3"
		if resp != expected {
			t.Fatalf("expected %q, got %q", expected, resp)
		}
	})
}

func TestHandleChannelListCommand_NoChannels(t *testing.T) {
	withTestDB(t, func(_ *gorm.DB) {
		svc := services.NewUserService()
		resp, err := handleChannelListCommand(svc)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp != "No hay canales disponibles" {
			t.Fatalf("unexpected response: %s", resp)
		}
	})
}

func TestExecuteCommand_ChannelList(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		createChannel(t, db, "canal-1")
		createChannel(t, db, "canal-2")
		createChannel(t, db, "canal-3")
		user := createUser(t, db)

		resp, err := executeCommand(user, svc, qwen.CommandResult{Intent: "request_channel_list"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp == "" {
			t.Fatalf("expected non-empty response")
		}
	})
}

func TestExecuteCommand_ChannelConnectMissingParam(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		user := createUser(t, db)
		_, err := executeCommand(user, svc, qwen.CommandResult{Intent: "request_channel_connect"})
		if err == nil {
			t.Fatalf("expected error when channel not provided")
		}
	})
}

func TestExecuteCommand_ChannelConnectSuccess(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		channel := createChannel(t, db, "canal-4")
		user := createUser(t, db)

		oldMove := mockMoveClientToChannel
		mockMoveClientToChannel = func(uid uint, ch string) {
			if uid != user.ID || ch != "canal-4" {
				t.Fatalf("unexpected move: %d %s", uid, ch)
			}
		}
		defer func() { mockMoveClientToChannel = oldMove }()

		resp, err := executeCommand(user, svc, qwen.CommandResult{
			Intent:   "request_channel_connect",
			Channels: []string{channel.Code},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp != "Conectado al canal 4" {
			t.Fatalf("unexpected response: %s", resp)
		}

		var updated models.User
		if err := db.First(&updated, user.ID).Error; err != nil {
			t.Fatalf("fetch user: %v", err)
		}
		if updated.CurrentChannelID == nil || *updated.CurrentChannelID != channel.ID {
			t.Fatalf("user not connected in DB")
		}
	})
}

func TestExecuteCommand_ChannelDisconnect(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		channel := createChannel(t, db, "canal-6")
		user := createUser(t, db, func(u *models.User) {
			u.CurrentChannelID = &channel.ID
		})
		if err := db.Model(user).Association("CurrentChannel").Append(channel); err != nil {
			t.Fatalf("associate channel: %v", err)
		}
		if err := db.Create(&models.ChannelMembership{
			UserID:    user.ID,
			ChannelID: channel.ID,
			Active:    true,
			JoinedAt:  time.Now(),
		}).Error; err != nil {
			t.Fatalf("create membership: %v", err)
		}

		oldMove := mockMoveClientToChannel
		mockMoveClientToChannel = func(uid uint, ch string) {
			if uid != user.ID || ch != "" {
				t.Fatalf("unexpected move: %d %s", uid, ch)
			}
		}
		defer func() { mockMoveClientToChannel = oldMove }()

		resp, err := executeCommand(user, svc, qwen.CommandResult{Intent: "request_channel_disconnect"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp != "Desconectado del canal 6" {
			t.Fatalf("unexpected response: %s", resp)
		}

		var updated models.User
		if err := db.First(&updated, user.ID).Error; err != nil {
			t.Fatalf("fetch user: %v", err)
		}
		if updated.CurrentChannelID != nil {
			t.Fatalf("expected user disconnected")
		}

		var membership models.ChannelMembership
		if err := db.Where("user_id = ? AND channel_id = ?", user.ID, channel.ID).First(&membership).Error; err != nil {
			t.Fatalf("fetch membership: %v", err)
		}
		if membership.Active {
			t.Fatalf("membership still active")
		}
	})
}

func TestHandleAsConversation_NoChannel(t *testing.T) {
	rec := httptest.NewRecorder()
	user := &models.User{}
	handleAsConversation(rec, user, []byte("wavdata"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestHandleAsConversation_WithChannel(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		defer cleanupAudioQueue()

		channel := createChannel(t, db, "canal-1")
		user := createUser(t, db, func(u *models.User) {
			u.CurrentChannelID = &channel.ID
		})
		user.CurrentChannel = channel

		if err := db.Model(&models.User{}).Where("id = ?", user.ID).Updates(map[string]any{
			"current_channel_id": channel.ID,
			"last_active_at":     time.Now(),
		}).Error; err != nil {
			t.Fatalf("update user: %v", err)
		}
		if err := db.Create(&models.ChannelMembership{
			UserID:    user.ID,
			ChannelID: channel.ID,
			Active:    true,
			JoinedAt:  time.Now(),
		}).Error; err != nil {
			t.Fatalf("create membership: %v", err)
		}

		rec := httptest.NewRecorder()

		oldStart, oldBroadcast, oldStop := mockStartTransmission, mockBroadcastAudio, mockStopTransmission
		oldEnqueue, oldDequeue := mockEnqueueAudio, mockDequeueAudio
		oldGetActive := mockGetChannelActiveUsers
		defer func() {
			mockStartTransmission = oldStart
			mockBroadcastAudio = oldBroadcast
			mockStopTransmission = oldStop
			mockEnqueueAudio = oldEnqueue
			mockDequeueAudio = oldDequeue
			mockGetChannelActiveUsers = oldGetActive
		}()

		mockStartTransmission = func(channel string, speaker uint) {
			if channel != "canal-1" || speaker != user.ID {
				t.Fatalf("unexpected startTransmission")
			}
		}
		mockBroadcastAudio = func(channel string, speaker uint, data []byte) {
			if channel != "canal-1" || speaker != user.ID {
				t.Fatalf("unexpected broadcast")
			}
		}
		mockStopTransmission = func(channel string, speaker uint) {
			if channel != "canal-1" || speaker != user.ID {
				t.Fatalf("unexpected stopTransmission")
			}
		}
		mockEnqueueAudio = func(sender uint, channel string, data []byte, duration float64, recipients []uint) {
			if sender != user.ID || channel != "canal-1" {
				t.Fatalf("unexpected enqueue")
			}
			if len(recipients) != 1 {
				t.Fatalf("unexpected recipients: %+v", recipients)
			}
		}
		mockDequeueAudio = func(uid uint) *PendingAudio {
			if uid != user.ID {
				t.Fatalf("unexpected dequeue user")
			}
			return &PendingAudio{
				SenderID:  99,
				Channel:   "canal-1",
				AudioData: []byte("reply"),
			}
		}
		mockGetChannelActiveUsers = func(_ *services.UserService, code string) ([]models.User, error) {
			if code != "canal-1" {
				t.Fatalf("unexpected channel code")
			}
			return []models.User{
				{Model: gorm.Model{ID: user.ID}},
				{Model: gorm.Model{ID: 55}},
			}, nil
		}

		handleAsConversation(rec, user, fakeWAV())

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
	})
}

func TestHandleAsConversation_WithChannel_ErrorGettingUsers(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		channel := createChannel(t, db, "canal-1")
		user := createUser(t, db, func(u *models.User) {
			u.CurrentChannelID = &channel.ID
		})

		rec := httptest.NewRecorder()

		oldGetActive := mockGetChannelActiveUsers
		defer func() { mockGetChannelActiveUsers = oldGetActive }()

		mockGetChannelActiveUsers = func(_ *services.UserService, code string) ([]models.User, error) {
			return nil, fmt.Errorf("db error")
		}

		handleAsConversation(rec, user, fakeWAV())

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204 on error, got %d", rec.Code)
		}
	})
}

func TestHandleAsConversation_WithChannel_WithPendingAudio(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		defer cleanupAudioQueue()

		channel := createChannel(t, db, "canal-1")
		user := createUser(t, db, func(u *models.User) {
			u.CurrentChannelID = &channel.ID
		})

		rec := httptest.NewRecorder()

		oldDequeue := mockDequeueAudio
		oldGetActive := mockGetChannelActiveUsers
		defer func() {
			mockDequeueAudio = oldDequeue
			mockGetChannelActiveUsers = oldGetActive
		}()

		mockDequeueAudio = func(uid uint) *PendingAudio {
			if uid != user.ID {
				t.Fatalf("unexpected dequeue user")
			}
			return &PendingAudio{
				SenderID:  99,
				Channel:   "canal-1",
				AudioData: []byte("reply audio"),
			}
		}
		mockGetChannelActiveUsers = func(_ *services.UserService, code string) ([]models.User, error) {
			return []models.User{{Model: gorm.Model{ID: user.ID}}}, nil
		}

		handleAsConversation(rec, user, fakeWAV())

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204 on error, got %d", rec.Code)
		}
	})
}

func TestReadUserIDHeader_OK(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		user := createUser(t, db)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Auth-Token", user.AuthToken)
		id, ok := readUserIDHeader(req)
		if !ok || id != user.ID {
			t.Fatalf("expected %d, got %d ok=%v", user.ID, id, ok)
		}
	})
}

func TestReadUserIDHeader_Missing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := readUserIDHeader(req); ok {
		t.Fatalf("expected false when token missing")
	}
}

func TestReadAudioFromRequest_Multipart(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write([]byte("content")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	data, err := readAudioFromRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("unexpected data: %s", string(data))
	}
}

func TestReadAudioFromRequest_Raw(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("rawdata")))
	req.Header.Set("Content-Type", "audio/wav")
	data, err := readAudioFromRequest(req)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(data) != "rawdata" {
		t.Fatalf("unexpected data: %s", string(data))
	}
}

func TestEstimateAudioDuration(t *testing.T) {
	wav := fakeWAV()
	duration := estimateAudioDuration(wav)
	if duration < 500*time.Millisecond || duration > 30*time.Second {
		t.Fatalf("unexpected duration: %v", duration)
	}

	large := make([]byte, 32000*40+44)
	copy(large, wav)
	if d := estimateAudioDuration(large); d > 30*time.Second {
		t.Fatalf("expected cap at 30s, got %v", d)
	}

	small := make([]byte, 44+10)
	copy(small, wav[:44])
	if d := estimateAudioDuration(small); d < 500*time.Millisecond {
		t.Fatalf("expected min 0.5s, got %v", d)
	}
}

func TestIsValidWAVFormat(t *testing.T) {
	if !isValidWAVFormat(fakeWAV()) {
		t.Fatalf("expected valid WAV")
	}
	if isValidWAVFormat([]byte("invalid")) {
		t.Fatalf("expected invalid WAV")
	}
}

func TestIsLikelyCoherent(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Hola mundo", true},
		{"sí", true},
		{"no", true},
		{"ok", true},
		{"vale", true},
		{"bien", true},
		{"abc", true},
		{"qwerty", true},
		{"a b c", true},
		{"aa", false},
		{"", false},
		{"h", false},
		{"Hola", true},
		{"Esto es una frase coherente", true},
		{"123", false},
		{"!@#", false},
		{"Sí, claro", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isLikelyCoherent(tt.input); got != tt.expected {
				t.Errorf("isLikelyCoherent(%q) = %v; want %v", tt.input, got, tt.expected)
			}
		})
	}
}
