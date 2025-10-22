// internal/http/handlers/audio_test.go
package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/services"
	"walkie-backend/pkg/deepseek"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var userCounter uint64

func TestAudioPoll_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/poll", nil)
	rec := httptest.NewRecorder()

	AudioPoll(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestRunAudioIngest_MissingToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID: func(*http.Request) (uint, bool) { return 0, false },
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestRunAudioIngest_ReadAudioError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", nil)
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 1, true },
		withTimeout: passthroughTimeout,
		readAudio: func(*http.Request) ([]byte, error) {
			return nil, errors.New("read error")
		},
		validateWAV: func([]byte) bool { return true },
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestRunAudioIngest_UserServiceNil(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:     func(*http.Request) (uint, bool) { return 1, true },
		withTimeout:    passthroughTimeout,
		readAudio:      func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV:    func([]byte) bool { return true },
		newUserService: func() userService { return nil },
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected %d, got %d", http.StatusInternalServerError, rec.Code)
	}
}

func TestRunAudioIngest_UserNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 5, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{userErr: errors.New("not found")}
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestRunAudioIngest_STTErrorWithoutChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 42, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{user: &models.User{}}
		},
		ensureSTT: func() (sttClient, error) {
			return &fakeSTT{err: errors.New("stt down")}, nil
		},
		handleConversation: func(http.ResponseWriter, *models.User, []byte) {
			t.Fatalf("conversation handler should not be called")
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRunAudioIngest_DeepseekErrorWithoutChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 11, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{user: &models.User{}}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return nil, errors.New("deepseek down")
		},
		handleConversation: func(http.ResponseWriter, *models.User, []byte) {
			t.Fatalf("conversation handler should not be called")
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRunAudioIngest_GetChannelsErrorWithoutChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 12, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user:        &models.User{},
				channelsErr: errors.New("db error"),
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{result: deepseek.CommandResult{}}, nil
		},
		handleConversation: func(http.ResponseWriter, *models.User, []byte) {
			t.Fatalf("conversation handler should not be called")
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRunAudioIngest_AnalyzeErrorWithoutChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 13, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user: &models.User{},
				channels: []models.Channel{
					{Code: "canal-1"},
				},
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{err: errors.New("analysis fail")}, nil
		},
		handleConversation: func(http.ResponseWriter, *models.User, []byte) {
			t.Fatalf("conversation handler should not be called")
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRunAudioIngest_CommandSuccess(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	user := &models.User{}
	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 7, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user:     user,
				channels: []models.Channel{{Code: "canal-1"}},
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "tráeme la lista de canales"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{
				result: deepseek.CommandResult{
					IsCommand: true,
					Intent:    "request_channel_list",
				},
			}, nil
		},
		handleConversation: func(http.ResponseWriter, *models.User, []byte) {
			t.Fatalf("conversation handler should not be called")
		},
		executeCommand: func(*models.User, *services.UserService, deepseek.CommandResult) (string, error) {
			return "ok", nil
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
	if body := rec.Body.String(); body != "ok" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestRunAudioIngest_NonCommandNoChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 21, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user:     &models.User{},
				channels: []models.Channel{{Code: "canal-1"}},
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{result: deepseek.CommandResult{IsCommand: false}}, nil
		},
		handleConversation: func(http.ResponseWriter, *models.User, []byte) {
			t.Fatalf("conversation handler should not be called")
		},
		executeCommand: func(*models.User, *services.UserService, deepseek.CommandResult) (string, error) {
			t.Fatalf("should not execute command")
			return "", nil
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRunAudioIngest_ConversationFallbackInChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	channelID := uint(1)
	user := &models.User{
		CurrentChannelID: &channelID,
		CurrentChannel:   &models.Channel{Code: "canal-1"},
	}

	conversationCalled := false

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 9, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user:     user,
				channels: []models.Channel{{Code: "canal-1"}},
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola equipo"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{result: deepseek.CommandResult{IsCommand: false}}, nil
		},
		handleConversation: func(w http.ResponseWriter, _ *models.User, _ []byte) {
			conversationCalled = true
			w.WriteHeader(http.StatusNoContent)
		},
		executeCommand: func(*models.User, *services.UserService, deepseek.CommandResult) (string, error) {
			t.Fatalf("command should not be executed")
			return "", nil
		},
	}

	runAudioIngest(rec, req, deps)

	if !conversationCalled {
		t.Fatalf("expected conversation handler to be invoked")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestAudioPoll_NoAudio(t *testing.T) {
	user := setupAuthUser(t)

	resetAudioQueue(t)

	req := httptest.NewRequest(http.MethodGet, "/audio/poll", nil)
	req.Header.Set("X-Auth-Token", user.AuthToken)
	rec := httptest.NewRecorder()

	AudioPoll(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestAudioPoll_WithAudio(t *testing.T) {
	user := setupAuthUser(t)

	resetAudioQueue(t)
	audioBytes := []byte{0x01, 0x02, 0x03}

	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues[user.ID] = []*PendingAudio{
		{
			SenderID:  99,
			Channel:   "canal-3",
			AudioData: audioBytes,
		},
	}
	globalAudioQueue.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/audio/poll", nil)
	req.Header.Set("X-Auth-Token", user.AuthToken)
	rec := httptest.NewRecorder()

	AudioPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("unexpected content-type %q", got)
	}
	if rec.Header().Get("X-Audio-From") != "99" || rec.Header().Get("X-Channel") != "canal-3" {
		t.Fatalf("headers not set correctly")
	}
	if !bytes.Equal(rec.Body.Bytes(), audioBytes) {
		t.Fatalf("unexpected body: %v", rec.Body.Bytes())
	}
	globalAudioQueue.mu.RLock()
	defer globalAudioQueue.mu.RUnlock()
	if len(globalAudioQueue.queues[user.ID]) != 0 {
		t.Fatalf("queue not cleared")
	}
}

func passthroughTimeout(ctx context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

func fakeWAV() []byte {
	data := make([]byte, 60)
	copy(data[0:], []byte("RIFF"))
	copy(data[8:], []byte("WAVE"))
	return data
}

type fakeUserService struct {
	user        *models.User
	userErr     error
	channels    []models.Channel
	channelsErr error
}

func (f *fakeUserService) GetUserWithChannel(uint) (*models.User, error) {
	if f.userErr != nil {
		return nil, f.userErr
	}
	return f.user, nil
}

func (f *fakeUserService) GetAvailableChannels() ([]models.Channel, error) {
	if f.channelsErr != nil {
		return nil, f.channelsErr
	}
	return f.channels, nil
}

type fakeSTT struct {
	text string
	err  error
}

func (f *fakeSTT) TranscribeAudio(context.Context, []byte) (string, error) {
	return f.text, f.err
}

type fakeDeepseek struct {
	result deepseek.CommandResult
	err    error
}

func (f *fakeDeepseek) AnalyzeTranscript(context.Context, string, []string, string, string) (deepseek.CommandResult, error) {
	return f.result, f.err
}

func setupAuthUser(t *testing.T) models.User {
	t.Helper()

	id := atomic.AddUint64(&userCounter, 1)

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	display := fmt.Sprintf("tester-%d", id)
	token := fmt.Sprintf("test-token-%d", id)

	user := models.User{
		DisplayName:  display,
		AuthToken:    token,
		IsActive:     true,
		LastActiveAt: time.Now(),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	oldDB := config.DB
	config.DB = db
	t.Cleanup(func() { config.DB = oldDB })

	return user
}

func resetAudioQueue(t *testing.T) {
	t.Helper()
	old := globalAudioQueue
	globalAudioQueue = &AudioQueue{queues: make(map[uint][]*PendingAudio)}
	t.Cleanup(func() { globalAudioQueue = old })
}

func TestRunAudioIngest_InvalidWAV(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader([]byte("bad")))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 1, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return []byte("bad"), nil },
		validateWAV: func([]byte) bool { return false },
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestRunAudioIngest_IncoherentText(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	deps := audioIngestDeps{
		readUserID:     func(*http.Request) (uint, bool) { return 2, true },
		withTimeout:    passthroughTimeout,
		readAudio:      func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV:    func([]byte) bool { return true },
		newUserService: func() userService { return &fakeUserService{user: &models.User{}} },
		ensureSTT:      func() (sttClient, error) { return &fakeSTT{text: "zz"}, nil },
		isCoherent:     func(string) bool { return false },
		handleConversation: func(http.ResponseWriter, *models.User, []byte) {
			t.Fatalf("conversation should not run")
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRunAudioIngest_STTErrorWithChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	channelID := uint(1)
	user := &models.User{
		CurrentChannelID: &channelID,
		CurrentChannel:   &models.Channel{Code: "canal-1"},
	}

	var called bool
	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 3, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{user: user, channels: []models.Channel{{Code: "canal-1"}}}
		},
		ensureSTT: func() (sttClient, error) { return &fakeSTT{err: errors.New("fail")}, nil },
		handleConversation: func(w http.ResponseWriter, _ *models.User, _ []byte) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	}

	runAudioIngest(rec, req, deps)

	if !called {
		t.Fatalf("conversation handler not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestRunAudioIngest_DeepseekErrorWithChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	channelID := uint(2)
	user := &models.User{
		CurrentChannelID: &channelID,
		CurrentChannel:   &models.Channel{Code: "canal-2"},
	}

	var called bool
	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 4, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{user: user, channels: []models.Channel{{Code: "canal-2"}}}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return nil, errors.New("down")
		},
		handleConversation: func(w http.ResponseWriter, _ *models.User, _ []byte) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	}

	runAudioIngest(rec, req, deps)

	if !called {
		t.Fatalf("conversation handler not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestRunAudioIngest_GetChannelsErrorWithChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	channelID := uint(3)
	user := &models.User{
		CurrentChannelID: &channelID,
		CurrentChannel:   &models.Channel{Code: "canal-3"},
	}

	var called bool
	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 5, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user:        user,
				channelsErr: errors.New("oops"),
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{result: deepseek.CommandResult{}}, nil
		},
		handleConversation: func(w http.ResponseWriter, _ *models.User, _ []byte) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	}

	runAudioIngest(rec, req, deps)

	if !called {
		t.Fatalf("conversation handler not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestRunAudioIngest_AnalyzeErrorWithChannel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	channelID := uint(4)
	user := &models.User{
		CurrentChannelID: &channelID,
		CurrentChannel:   &models.Channel{Code: "canal-4"},
	}

	var called bool
	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 6, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user:     user,
				channels: []models.Channel{{Code: "canal-4"}},
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "hola"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{err: errors.New("fail")}, nil
		},
		handleConversation: func(w http.ResponseWriter, _ *models.User, _ []byte) {
			called = true
			w.WriteHeader(http.StatusOK)
		},
	}

	runAudioIngest(rec, req, deps)

	if !called {
		t.Fatalf("conversation handler not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestRunAudioIngest_CommandExecError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(fakeWAV()))
	rec := httptest.NewRecorder()

	user := &models.User{}
	deps := audioIngestDeps{
		readUserID:  func(*http.Request) (uint, bool) { return 7, true },
		withTimeout: passthroughTimeout,
		readAudio:   func(*http.Request) ([]byte, error) { return fakeWAV(), nil },
		validateWAV: func([]byte) bool { return true },
		newUserService: func() userService {
			return &fakeUserService{
				user:     user,
				channels: []models.Channel{{Code: "canal-1"}},
			}
		},
		ensureSTT:  func() (sttClient, error) { return &fakeSTT{text: "tráeme la lista de canales"}, nil },
		isCoherent: func(string) bool { return true },
		ensureDeepseek: func() (deepseekClient, error) {
			return &fakeDeepseek{
				result: deepseek.CommandResult{IsCommand: true, Intent: "request_channel_list"},
			}, nil
		},
		executeCommand: func(*models.User, *services.UserService, deepseek.CommandResult) (string, error) {
			return "", errors.New("boom")
		},
	}

	runAudioIngest(rec, req, deps)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestAudioPoll_Unauthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/audio/poll", nil)
	rec := httptest.NewRecorder()

	AudioPoll(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}
