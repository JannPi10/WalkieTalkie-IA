package handlers

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/services"
	"walkie-backend/pkg/qwen"

	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// MockUserService para simular el servicio de usuarios
type MockUserService struct {
	GetAvailableChannelsFunc             func() ([]models.Channel, error)
	ConnectUserToChannelFunc             func(userID uint, channelCode string) error
	DisconnectUserFromCurrentChannelFunc func(userID uint) error
	GetChannelActiveUsersFunc            func(channelCode string) ([]models.User, error)
}

func (m *MockUserService) GetAvailableChannels() ([]models.Channel, error) {
	return m.GetAvailableChannelsFunc()
}

func (m *MockUserService) ConnectUserToChannel(userID uint, channelCode string) error {
	return m.ConnectUserToChannelFunc(userID, channelCode)
}

func (m *MockUserService) DisconnectUserFromCurrentChannel(userID uint) error {
	return m.DisconnectUserFromCurrentChannelFunc(userID)
}

func (m *MockUserService) GetChannelActiveUsers(channelCode string) ([]models.User, error) {
	return m.GetChannelActiveUsersFunc(channelCode)
}

var helperCounter uint64

// withTestDB crea una base de datos en memoria para pruebas
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

// createChannel es un helper para crear canales de prueba
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

// createUser es un helper para crear usuarios de prueba
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

// TestHandleChannelListCommand_ReturnsList verifica el comando de listar canales
func TestHandleChannelListCommand_ReturnsList(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		createChannel(t, db, "canal-1")
		createChannel(t, db, "canal-2")
		createChannel(t, db, "canal-3")

		resp, err := handleChannelListCommand(svc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assert.Equal(t, "ok", resp.Status)
		assert.Equal(t, "request_channel_list", resp.Intent)
		assert.Contains(t, resp.Message, "Canales disponibles")
		assert.Len(t, resp.Data["channels"].([]string), 3)
		assert.Len(t, resp.Data["channel_names"].([]string), 3)
	})
}

// TestHandleChannelConnectCommand_Success verifica la conexión exitosa a un canal
func TestHandleChannelConnectCommand_Success(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		createChannel(t, db, "canal-1")
		user := createUser(t, db)

		// Llamamos directamente a la función que queremos probar
		resp, err := handleChannelConnectCommand(user, svc, "canal-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verificamos que la respuesta sea la esperada
		assert.Equal(t, "ok", resp.Status)
		assert.Equal(t, "request_channel_connect", resp.Intent)
		assert.Equal(t, "Conectado al canal 1", resp.Message)

		// Verificamos que el usuario esté en el canal correcto
		membership := &models.ChannelMembership{}
		if err := db.Where("user_id = ?", user.ID).First(membership).Error; err != nil {
			t.Fatalf("no se pudo obtener la membresía del canal: %v", err)
		}

		channel := &models.Channel{}
		if err := db.First(channel, membership.ChannelID).Error; err != nil {
			t.Fatalf("no se pudo obtener el canal: %v", err)
		}

		assert.Equal(t, "canal-1", channel.Code)
	})
}

// TestHandleChannelDisconnectCommand_NotInChannel verifica el intento de desconexión cuando no se está en un canal
func TestHandleChannelDisconnectCommand_NotInChannel(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		user := createUser(t, db)

		resp, err := handleChannelDisconnectCommand(user, svc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assert.Equal(t, "ok", resp.Status)
		assert.Equal(t, "request_channel_disconnect", resp.Intent)
		assert.Equal(t, "No estás conectado a ningún canal", resp.Message)
	})
}

// TestExecuteCommand_ChannelList verifica el comando de lista de canales a través de executeCommand
func TestExecuteCommand_ChannelList(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		createChannel(t, db, "canal-1")
		createChannel(t, db, "canal-2")
		user := createUser(t, db)

		resp, err := executeCommand(user, svc, qwen.CommandResult{
			IsCommand: true,
			Intent:    "request_channel_list",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assert.Equal(t, "ok", resp.Status)
		assert.Equal(t, "request_channel_list", resp.Intent)
		assert.Contains(t, resp.Message, "Canales disponibles")
	})
}

// TestExecuteCommand_UnknownCommand verifica el manejo de comandos desconocidos
func TestExecuteCommand_UnknownCommand(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		user := createUser(t, db)

		resp, err := executeCommand(user, svc, qwen.CommandResult{
			IsCommand: true,
			Intent:    "unknown_command",
			Reply:     "Respuesta predeterminada",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assert.Equal(t, "ok", resp.Status)
		assert.Equal(t, "unknown_command", resp.Intent)
		assert.Equal(t, "Respuesta predeterminada", resp.Message)
	})
}

// TestBuildChannelListPhrase verifica la construcción de frases para listas de canales
func TestBuildChannelListPhrase(t *testing.T) {
	tests := []struct {
		name     string
		channels []string
		want     string
	}{
		{"no channels", []string{}, "No hay canales disponibles"},
		{"one channel", []string{"1"}, "Canales disponibles: 1"},
		{"two channels", []string{"1", "2"}, "Canales disponibles: 1 y 2"},
		{"three channels", []string{"1", "2", "3"}, "Canales disponibles: 1, 2, y 3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildChannelListPhrase(tt.channels)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHandleChannelDisconnectCommand_Success(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		ch := createChannel(t, db, "canal-1")
		user := createUser(t, db, func(u *models.User) {
			u.CurrentChannelID = &ch.ID
			u.CurrentChannel = ch
		})

		resp, err := handleChannelDisconnectCommand(user, svc)
		assert.NoError(t, err)
		assert.Equal(t, "ok", resp.Status)
		assert.Equal(t, "request_channel_disconnect", resp.Intent)
		assert.Equal(t, "Desconectado del canal 1", resp.Message)

		// Verify user is disconnected in DB
		updatedUser := &models.User{}
		db.First(updatedUser, user.ID)
		assert.Nil(t, updatedUser.CurrentChannelID)
	})
}

func TestExecuteCommand_ConnectAndDisconnect(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		svc := services.NewUserService()
		createChannel(t, db, "canal-5")
		user := createUser(t, db)

		// Connect
		connectResp, err := executeCommand(user, svc, qwen.CommandResult{
			IsCommand: true,
			Intent:    "request_channel_connect",
			Channels:  []string{"canal-5"},
		})
		assert.NoError(t, err)
		assert.Equal(t, "Conectado al canal 5", connectResp.Message)

		// Update user object for disconnect
		db.Preload("CurrentChannel").First(user, user.ID)

		// Disconnect
		disconnectResp, err := executeCommand(user, svc, qwen.CommandResult{
			IsCommand: true,
			Intent:    "request_channel_disconnect",
		})
		assert.NoError(t, err)
		assert.Equal(t, "Desconectado del canal 5", disconnectResp.Message)
	})
}

func TestFindUserByToken(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		t.Setenv("AUTH_TOKEN_TTL", "1h")
		activeUser := createUser(t, db, func(u *models.User) {
			u.AuthToken = "active-token"
			u.LastActiveAt = time.Now()
		})
		createUser(t, db, func(u *models.User) {
			u.AuthToken = "expired-token"
			u.LastActiveAt = time.Now().Add(-2 * time.Hour)
		})

		t.Run("valid token", func(t *testing.T) {
			user, err := findUserByToken("active-token")
			assert.NoError(t, err)
			assert.Equal(t, activeUser.ID, user.ID)
		})

		t.Run("token not found", func(t *testing.T) {
			_, err := findUserByToken("non-existent-token")
			assert.Error(t, err)
		})

		t.Run("expired token", func(t *testing.T) {
			_, err := findUserByToken("expired-token")
			assert.Error(t, err)
			assert.Equal(t, "token expirado", err.Error())
		})
	})
}

func TestIsValidWAVFormat(t *testing.T) {
	t.Run("valid wav", func(t *testing.T) {
		validHeader := []byte("RIFFxxxxWAVEfmt ")
		validFile := append(validHeader, make([]byte, 44-len(validHeader))...)
		assert.True(t, isValidWAVFormat(validFile))
	})

	t.Run("invalid format", func(t *testing.T) {
		assert.False(t, isValidWAVFormat([]byte("invalid")))
	})

	t.Run("too short", func(t *testing.T) {
		assert.False(t, isValidWAVFormat([]byte("RIFF")))
	})
}

func TestIsLikelyCoherent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", " ", false},
		{"short common word", "ok", true},
		{"short non-common word", "yo", false},
		{"short garbage", "zxc", false},
		{"coherent sentence", "hola, cómo estás?", true},
		{"incoherent sentence", "sdfg cvb rty", false},
		{"no vowels", "rhythm", false},
		{"just numbers", "12345", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isLikelyCoherent(tt.input))
		})
	}
}

func TestEstimateAudioDuration(t *testing.T) {
	t.Run("standard case", func(t *testing.T) {
		// 1 second of audio at 16kHz, 16-bit mono (32000 bytes)
		audio := make([]byte, 32000)
		duration := estimateAudioDuration(audio)
		assert.InDelta(t, 1*time.Second, duration, float64(50*time.Millisecond))
	})

	t.Run("with wav header", func(t *testing.T) {
		audio := make([]byte, 32000+44)
		copy(audio[0:4], "RIFF")
		copy(audio[8:12], "WAVE")
		duration := estimateAudioDuration(audio)
		assert.InDelta(t, 1*time.Second, duration, float64(50*time.Millisecond))
	})

	t.Run("min duration", func(t *testing.T) {
		audio := make([]byte, 100)
		duration := estimateAudioDuration(audio)
		assert.Equal(t, 500*time.Millisecond, duration)
	})

	t.Run("max duration", func(t *testing.T) {
		audio := make([]byte, 1000000) // > 30s
		duration := estimateAudioDuration(audio)
		assert.Equal(t, 30*time.Second, duration)
	})
}

func TestReadAudioFromRequest(t *testing.T) {
	t.Run("plain body", func(t *testing.T) {
		body := "plain audio data"
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "audio/wav")
		audio, _, err := readAudioFromRequest(req)
		assert.NoError(t, err)
		assert.Equal(t, body, string(audio))
	})

	t.Run("multipart form", func(t *testing.T) {
		body := new(bytes.Buffer)
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("audio", "test.wav")
		assert.NoError(t, err)
		_, err = io.WriteString(part, "multipart audio data")
		assert.NoError(t, err)
		writer.Close()

		req := httptest.NewRequest("POST", "/", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())

		audio, _, err := readAudioFromRequest(req)
		assert.NoError(t, err)
		assert.Equal(t, "multipart audio data", string(audio))
	})
}

func TestAuthTokenTTL(t *testing.T) {
	t.Run("valid duration", func(t *testing.T) {
		os.Clearenv()
		t.Setenv("AUTH_TOKEN_TTL", "2h30m")
		tokenTTLOnce = *new(sync.Once)
		duration := authTokenTTL()
		assert.Equal(t, 2*time.Hour+30*time.Minute, duration)
	})

	t.Run("invalid duration", func(t *testing.T) {
		os.Clearenv()
		t.Setenv("AUTH_TOKEN_TTL", "invalid")
		tokenTTLOnce = *new(sync.Once)
		duration := authTokenTTL()
		assert.Equal(t, 24*time.Hour, duration)
	})

	t.Run("empty duration", func(t *testing.T) {
		os.Clearenv()
		tokenTTLOnce = *new(sync.Once)
		duration := authTokenTTL()
		assert.Equal(t, 24*time.Hour, duration)
	})
}

func TestResolveUserFromRequest(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		user := createUser(t, db, func(u *models.User) {
			u.AuthToken = "the-token"
		})

		t.Run("valid token", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Auth-Token", "the-token")
			resolvedUser, err := resolveUserFromRequest(req)
			assert.NoError(t, err)
			assert.Equal(t, user.ID, resolvedUser.ID)
		})

		t.Run("invalid token", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Auth-Token", "invalid-token")
			_, err := resolveUserFromRequest(req)
			assert.Error(t, err)
		})
	})
}

func TestHandleAsConversation(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		ch := createChannel(t, db, "conv-test")
		svc := services.NewUserService()
		sender := createUser(t, db)
		receiver := createUser(t, db)

		err := svc.ConnectUserToChannel(sender.ID, ch.Code)
		assert.NoError(t, err)
		err = svc.ConnectUserToChannel(receiver.ID, ch.Code)
		assert.NoError(t, err)

		db.Preload("CurrentChannel").First(sender, sender.ID)

		receiverClient := &wsClient{userID: receiver.ID, channel: ch.Code, send: make(chan []byte, 1)}
		registerClient(receiverClient)
		defer removeClient(receiverClient)

		t.Run("successful conversation", func(t *testing.T) {
			w := httptest.NewRecorder()
			audioData := []byte("test audio")
			handleAsConversation(w, sender, audioData)

			assert.Equal(t, http.StatusNoContent, w.Code)

			assert.Eventually(t, func() bool {
				globalAudioQueue.mu.RLock()
				defer globalAudioQueue.mu.RUnlock()
				queue, ok := globalAudioQueue.queues[receiver.ID]
				return ok && len(queue) > 0 && queue[0].SenderID == sender.ID
			}, 100*time.Millisecond, 10*time.Millisecond, "audio was not enqueued for receiver")
		})

		t.Run("user not in channel", func(t *testing.T) {
			userNotInChannel := createUser(t, db)
			w := httptest.NewRecorder()
			handleAsConversation(w, userNotInChannel, []byte("audio"))
			assert.Equal(t, http.StatusNoContent, w.Code)
		})

		t.Run("no other users in channel", func(t *testing.T) {
			soloUser := createUser(t, db)
			soloChan := createChannel(t, db, "solo-chan")
			err := svc.ConnectUserToChannel(soloUser.ID, soloChan.Code)
			assert.NoError(t, err)
			db.Preload("CurrentChannel").First(soloUser, soloUser.ID)

			w := httptest.NewRecorder()
			handleAsConversation(w, soloUser, []byte("audio"))
			assert.Equal(t, http.StatusNoContent, w.Code)

			// Ensure no audio was queued for anyone
			time.Sleep(50 * time.Millisecond) // give time for any goroutines to run
			globalAudioQueue.mu.RLock()
			defer globalAudioQueue.mu.RUnlock()
			assert.Empty(t, globalAudioQueue.queues[soloUser.ID])
		})
	})
}

func TestReadUserIDHeader(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		user := createUser(t, db, func(u *models.User) {
			u.AuthToken = "test-token"
		})

		t.Run("valid token", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Auth-Token", "test-token")
			userID, err := readUserIDHeader(req)
			assert.NoError(t, err)
			assert.Equal(t, user.ID, userID)
		})

		t.Run("invalid token", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Auth-Token", "invalid")
			userID, err := readUserIDHeader(req)
			assert.Error(t, err)
			assert.Zero(t, userID)
		})

		t.Run("empty token", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Auth-Token", "")
			userID, err := readUserIDHeader(req)
			assert.Error(t, err)
			assert.Zero(t, userID)
		})
	})
}

func TestResolveUserFromRequest_UpdatesActivity(t *testing.T) {
	withTestDB(t, func(db *gorm.DB) {
		user := createUser(t, db, func(u *models.User) {
			u.AuthToken = "activity-token"
			u.LastActiveAt = time.Now().Add(-5 * time.Minute)
		})
		initialActivity := user.LastActiveAt

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Auth-Token", "activity-token")

		resolvedUser, err := resolveUserFromRequest(req)
		assert.NoError(t, err)
		assert.Equal(t, user.ID, resolvedUser.ID)

		// Check that LastActiveAt was updated in the database
		var updatedUser models.User
		db.First(&updatedUser, user.ID)
		assert.True(t, updatedUser.LastActiveAt.After(initialActivity))
	})
}
