package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"walkie-backend/internal/models"
	"walkie-backend/internal/services"
	"walkie-backend/pkg/deepseek"
)

type userService interface {
	GetUserWithChannel(uint) (*models.User, error)
	GetAvailableChannels() ([]models.Channel, error)
}

type sttClient interface {
	TranscribeAudio(context.Context, []byte) (string, error)
}

type deepseekClient interface {
	AnalyzeTranscript(context.Context, string, []string, string, string) (deepseek.CommandResult, error)
}

type audioIngestDeps struct {
	readUserID         func(*http.Request) (uint, bool)
	withTimeout        func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	readAudio          func(*http.Request) ([]byte, error)
	validateWAV        func([]byte) bool
	newUserService     func() userService
	ensureSTT          func() (sttClient, error)
	ensureDeepseek     func() (deepseekClient, error)
	isCoherent         func(string) bool
	handleConversation func(http.ResponseWriter, *models.User, []byte)
	executeCommand     func(*models.User, *services.UserService, deepseek.CommandResult) (string, error)
}

func newAudioIngestDeps() audioIngestDeps {
	return audioIngestDeps{
		readUserID:  readUserIDHeader,
		withTimeout: context.WithTimeout,
		readAudio:   readAudioFromRequest,
		validateWAV: isValidWAVFormat,
		newUserService: func() userService {
			return services.NewUserService()
		},
		ensureSTT: func() (sttClient, error) {
			return EnsureSTTClient()
		},
		ensureDeepseek: func() (deepseekClient, error) {
			return EnsureDeepseekClient()
		},
		isCoherent: isLikelyCoherent,
		handleConversation: func(w http.ResponseWriter, user *models.User, audio []byte) {
			handleAsConversation(w, user, audio)
		},
		executeCommand: func(user *models.User, svc *services.UserService, result deepseek.CommandResult) (string, error) {
			if svc == nil {
				return "", fmt.Errorf("servicio de usuarios no disponible")
			}
			return executeCommand(user, svc, result)
		},
	}
}

// POST /audio/ingest
// Headers: X-User-ID: <uint>
// Body: audio/wav (raw) o multipart/form-data; name=file
func AudioIngest(w http.ResponseWriter, r *http.Request) {
	runAudioIngest(w, r, newAudioIngestDeps())
}

func runAudioIngest(w http.ResponseWriter, r *http.Request, deps audioIngestDeps) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := deps.readUserID(r)
	if !ok {
		http.Error(w, "X-Auth-Token requerido", http.StatusBadRequest)
		return
	}

	ctx, cancel := deps.withTimeout(r.Context(), 120*time.Second)
	defer cancel()

	audioData, err := deps.readAudio(r)
	if err != nil || len(audioData) == 0 {
		log.Printf("Error leyendo audio de usuario %d: %v", userID, err)
		http.Error(w, "Audio requerido", http.StatusBadRequest)
		return
	}

	log.Printf("Audio recibido de usuario %d, tamaño: %d bytes", userID, len(audioData))

	if !deps.validateWAV(audioData) {
		log.Printf("Formato de audio inválido de usuario %d", userID)
		http.Error(w, "Formato de audio inválido. Se requiere WAV", http.StatusBadRequest)
		return
	}

	userSvc := deps.newUserService()
	if userSvc == nil {
		http.Error(w, "Servicio de usuarios no disponible", http.StatusInternalServerError)
		return
	}

	user, err := userSvc.GetUserWithChannel(userID)
	if err != nil {
		log.Printf("Usuario %d no encontrado: %v", userID, err)
		http.Error(w, "Usuario no encontrado", http.StatusNotFound)
		return
	}

	sttClient, err := deps.ensureSTT()
	if err != nil {
		log.Printf("STT no disponible para usuario %d: %v", userID, err)
		http.Error(w, "Servicio de transcripción no disponible", http.StatusServiceUnavailable)
		return
	}

	text, err := sttClient.TranscribeAudio(ctx, audioData)
	if err != nil {
		log.Printf("Error en STT para usuario %d: %v", userID, err)
		if user.IsInChannel() {
			log.Printf("Enviando audio sin transcripción para usuario %d (en canal)", userID)
			deps.handleConversation(w, user, audioData)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	text = strings.TrimSpace(text)
	log.Printf("Texto transcrito de usuario %d: '%s'", userID, text)

	if !deps.isCoherent(text) {
		log.Printf("Texto no coherente de usuario %d, ignorando", userID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	currentState := "sin_canal"
	if user.IsInChannel() {
		currentState = user.GetCurrentChannelCode()
	}

	log.Printf("Usuario %d en estado: %s", userID, currentState)

	dsClient, err := deps.ensureDeepseek()
	if err != nil {
		log.Printf("Deepseek no disponible para usuario %d: %v", userID, err)
		if user.IsInChannel() {
			deps.handleConversation(w, user, audioData)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	channels, err := userSvc.GetAvailableChannels()
	if err != nil {
		log.Printf("Error obteniendo canales para usuario %d: %v", userID, err)
		if user.IsInChannel() {
			deps.handleConversation(w, user, audioData)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	chanCodes := make([]string, len(channels))
	for i, ch := range channels {
		chanCodes[i] = ch.Code
	}

	result, err := dsClient.AnalyzeTranscript(ctx, text, chanCodes, currentState, "")
	if err != nil {
		log.Printf("Error analizando transcripción para usuario %d: %v", userID, err)
		if user.IsInChannel() {
			log.Printf("Fallback: tratando como conversación para usuario %d", userID)
			deps.handleConversation(w, user, audioData)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	log.Printf("Resultado análisis usuario %d: comando=%v, intent=%s", userID, result.IsCommand, result.Intent)

	if result.IsCommand {
		svcPtr, _ := userSvc.(*services.UserService)
		response, execErr := deps.executeCommand(user, svcPtr, result)
		if execErr != nil {
			log.Printf("Error ejecutando comando para usuario %d: %v", userID, execErr)
			http.Error(w, execErr.Error(), http.StatusBadRequest)
			return
		}

		log.Printf("Comando ejecutado para usuario %d, respuesta: %s", userID, response)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(response))
		return
	}

	if !user.IsInChannel() {
		log.Printf("Usuario %d no está en canal, ignorando conversación", userID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	deps.handleConversation(w, user, audioData)
}

// GET /audio/poll
// Headers: X-Auth-Token: <token>
func AudioPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := readUserIDHeader(r)
	if !ok {
		http.Error(w, "X-Auth-Token requerido", http.StatusUnauthorized)
		return
	}

	pendingAudio := DequeueAudio(userID)

	if pendingAudio != nil {
		log.Printf("Usuario %d recibe audio pendiente de usuario %d via polling", userID, pendingAudio.SenderID)

		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("X-Audio-From", fmt.Sprintf("%d", pendingAudio.SenderID))
		w.Header().Set("X-Channel", pendingAudio.Channel)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(pendingAudio.AudioData); err != nil {
			log.Printf("Error enviando audio a usuario %d: %v", userID, err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
