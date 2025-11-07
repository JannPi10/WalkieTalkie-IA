package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"walkie-backend/internal/models"
	"walkie-backend/pkg/qwen"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// mockUserService es un mock para la interfaz userService.
// No puede implementar services.UserService directamente debido a la conversión de tipo en el código original,
// pero puede satisfacer la interfaz userService para la mayoría de las dependencias.
type mockUserService struct {
	user        *models.User
	userErr     error
	channels    []models.Channel
	channelsErr error
}

func (m *mockUserService) GetUserWithChannel(id uint) (*models.User, error) {
	if m.userErr != nil {
		return nil, m.userErr
	}
	if m.user != nil && m.user.ID == id {
		return m.user, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockUserService) GetAvailableChannels() ([]models.Channel, error) {
	if m.channelsErr != nil {
		return nil, m.channelsErr
	}
	return m.channels, nil
}

// mockSTT es un mock para la interfaz sttClient.
type mockSTT struct {
	text   string
	err    error
	format string
}

func (m *mockSTT) TranscribeAudio(ctx context.Context, audio []byte, format string) (string, error) {
	m.format = format
	return m.text, m.err
}

// mockQwen es un mock para la interfaz qwenClient.
type mockQwen struct {
	result qwen.CommandResult
	err    error
}

func (m *mockQwen) AnalyzeTranscript(ctx context.Context, text string, channels []string, state string, pendingChannel string) (qwen.CommandResult, error) {
	return m.result, m.err
}

func TestAudioIngest_MethodNotAllowed(t *testing.T) {
	methods := []string{http.MethodGet, http.MethodPut, http.MethodDelete}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/audio/ingest", nil)
			rec := httptest.NewRecorder()
			AudioIngest(rec, req)
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		})
	}
}

func TestRunAudioIngest_InvalidInput(t *testing.T) {
	// Caso 1: Falla en la lectura del ID de usuario (token faltante)
	t.Run("missing_user_id", func(t *testing.T) {
		deps := newAudioIngestDeps()
		deps.readUserID = func(r *http.Request) (uint, error) {
			return 0, errors.New("missing token")
		}

		req := httptest.NewRequest(http.MethodPost, "/audio/ingest", nil)
		rec := httptest.NewRecorder()
		runAudioIngest(rec, req, deps)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "Error de autenticación")
	})

	// Caso 2: Usuario no encontrado en la base de datos
	t.Run("user_not_found", func(t *testing.T) {
		deps := newAudioIngestDeps()
		deps.readUserID = func(r *http.Request) (uint, error) { return 1, nil } // ID de usuario válido
		deps.newUserService = func() userService {
			return &mockUserService{userErr: gorm.ErrRecordNotFound} // Mock de servicio que devuelve error
		}

		req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader([]byte("RIFF...WAVE...")))
		req.Header.Set("Content-Type", "audio/wav") // Añadir Content-Type para evitar error de parseo
		rec := httptest.NewRecorder()
		deps.validateAudio = func(b []byte, format string) bool { return true } // Forzar validación de WAV
		runAudioIngest(rec, req, deps)

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "Usuario no encontrado")
	})

	// Caso 3: Formato de audio inválido
	t.Run("invalid_wav_format", func(t *testing.T) {
		deps := newAudioIngestDeps()
		deps.readUserID = func(r *http.Request) (uint, error) { return 1, nil } // Usuario existe
		deps.validateAudio = func(b []byte, format string) bool { return false }                 // Falla la validación de WAV

		req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader([]byte("not a wav")))
		req.Header.Set("Content-Type", "audio/wav") // Añadir Content-Type para que validateAudio se ejecute
		rec := httptest.NewRecorder()
		runAudioIngest(rec, req, deps)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "Formato de audio inválido")
	})
}

func TestRunAudioIngest_CommandFlow(t *testing.T) {
	mockUser := &models.User{Model: gorm.Model{ID: 1}, DisplayName: "test"}

	deps := newAudioIngestDeps()
	deps.readUserID = func(*http.Request) (uint, error) { return 1, nil }
	deps.newUserService = func() userService {
		return &mockUserService{user: mockUser}
	}
	deps.ensureSTT = func() (sttClient, error) { return &mockSTT{text: "dame la lista de canales"}, nil }
	deps.ensureAI = func() (qwenClient, error) {
		return &mockQwen{result: qwen.CommandResult{IsCommand: true, Intent: "request_channel_list"}}, nil
	}
	deps.validateAudio = func([]byte, string) bool { return true }
	deps.readAudio = func(*http.Request) ([]byte, string, error) { return []byte("audio data"), "audio/wav", nil }

	// Mock para executeCommand para evitar la conversión de tipo
	deps.executeCommand = func(user *models.User, svc userService, result qwen.CommandResult) (CommandResponse, error) {
		assert.Equal(t, "request_channel_list", result.Intent)
		return CommandResponse{Status: "ok", Intent: "request_channel_list", Message: "Canales: 1, 2"}, nil
	}

	req := httptest.NewRequest(http.MethodPost, "/audio/ingest", bytes.NewReader(nil))
	rec := httptest.NewRecorder()

	runAudioIngest(rec, req, deps)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Canales: 1, 2")
}



func TestAudioPoll_Unauthorized(t *testing.T) {
	deps := newAudioPollDeps()
	deps.resolveUser = func(r *http.Request) (*models.User, error) {
		return nil, errors.New("invalid token")
	}

	req := httptest.NewRequest(http.MethodGet, "/audio/poll", nil)
	rec := httptest.NewRecorder()

	runAudioPoll(rec, req, deps)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAudioPoll_NoAudioAvailable(t *testing.T) {
	mockUser := &models.User{Model: gorm.Model{ID: 1}}

	deps := newAudioPollDeps()
	deps.resolveUser = func(r *http.Request) (*models.User, error) {
		return mockUser, nil
	}
	// Mock DequeueAudio para que devuelva nil (sin audio pendiente)
	deps.dequeueAudio = func(userID uint) *PendingAudio {
		return nil
	}

	req := httptest.NewRequest(http.MethodGet, "/audio/poll", nil)
	req.Header.Set("X-Auth-Token", "valid-token")
	rec := httptest.NewRecorder()

	runAudioPoll(rec, req, deps)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestAudioPoll_AudioAvailable(t *testing.T) {
	mockUser := &models.User{Model: gorm.Model{ID: 1}, CurrentChannel: &models.Channel{Code: "general"}}

	deps := newAudioPollDeps()
	deps.resolveUser = func(r *http.Request) (*models.User, error) {
		return mockUser, nil
	}
	// Mock DequeueAudio para que devuelva un audio pendiente
	deps.dequeueAudio = func(userID uint) *PendingAudio {
		return &PendingAudio{
			SenderID:  2,
			Channel:   "general",
			AudioData: []byte("audio content"),
		}
	}
	// Mock del servicio de usuario para confirmar que el usuario sigue en el canal
	deps.newUserService = func() userService {
		return &mockUserService{user: mockUser}
	}

	req := httptest.NewRequest(http.MethodGet, "/audio/poll", nil)
	req.Header.Set("X-Auth-Token", "valid-token")
	rec := httptest.NewRecorder()

	runAudioPoll(rec, req, deps)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "audio/wav", rec.Header().Get("Content-Type"))
	assert.Equal(t, "2", rec.Header().Get("X-Audio-From"))
	assert.Equal(t, "general", rec.Header().Get("X-Channel"))
	assert.Equal(t, "audio content", rec.Body.String())
}

func TestAudioPoll_UserChangedChannel(t *testing.T) {
	mockUser := &models.User{Model: gorm.Model{ID: 1}, CurrentChannel: &models.Channel{Code: "other"}}

	deps := newAudioPollDeps()
	deps.resolveUser = func(r *http.Request) (*models.User, error) {
		return &models.User{Model: gorm.Model{ID: 1}}, nil // Devuelve un usuario sin canal actual
	}

	// Mock DequeueAudio para que devuelva un audio una sola vez.
	var dequeued bool
	deps.dequeueAudio = func(userID uint) *PendingAudio {
		if !dequeued {
			dequeued = true
			return &PendingAudio{Channel: "general"} // Audio del canal anterior
		}
		return nil // Sin más audios pendientes
	}

	deps.newUserService = func() userService {
		// El servicio devuelve que el usuario ahora está en el canal "other"
		return &mockUserService{user: mockUser}
	}

	req := httptest.NewRequest(http.MethodGet, "/audio/poll", nil)
	rec := httptest.NewRecorder()

	runAudioPoll(rec, req, deps)

	// El audio se descarta y no se envía nada.
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
