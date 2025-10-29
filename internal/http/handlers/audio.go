package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"walkie-backend/internal/models"
	"walkie-backend/internal/services"
	"walkie-backend/pkg/qwen"
)

type userService interface {
	GetUserWithChannel(uint) (*models.User, error)
	GetAvailableChannels() ([]models.Channel, error)
}

type sttClient interface {
	TranscribeAudio(context.Context, []byte) (string, error)
}

type qwenClient interface {
	AnalyzeTranscript(context.Context, string, []string, string, string) (qwen.CommandResult, error)
}

type audioIngestDeps struct {
	readUserID         func(*http.Request) (uint, bool)
	withTimeout        func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	readAudio          func(*http.Request) ([]byte, error)
	validateWAV        func([]byte) bool
	newUserService     func() userService
	ensureSTT          func() (sttClient, error)
	ensureAI           func() (qwenClient, error)
	isCoherent         func(string) bool
	handleConversation func(http.ResponseWriter, *models.User, []byte)
	executeCommand     func(*models.User, *services.UserService, qwen.CommandResult) (CommandResponse, error)
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
		ensureAI: func() (qwenClient, error) {
			return EnsureAIClient()
		},
		isCoherent: isLikelyCoherent,
		handleConversation: func(w http.ResponseWriter, user *models.User, audio []byte) {
			handleAsConversation(w, user, audio)
		},
		executeCommand: func(user *models.User, svc *services.UserService, result qwen.CommandResult) (CommandResponse, error) {
			if svc == nil {
				return CommandResponse{}, fmt.Errorf("servicio de usuarios no disponible")
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
		} else {
			writeUnintelligibleResponse(w)
		}
		return
	}

	if !deps.isCoherent(text) {
		log.Printf("Texto no coherente de usuario %d, ignorando", userID)
		if user.IsInChannel() {
			w.WriteHeader(http.StatusNoContent)
		} else {
			writeUnintelligibleResponse(w)
		}
		return
	}

	currentState := "sin_canal"
	if user.IsInChannel() {
		currentState = user.GetCurrentChannelCode()
	}

	log.Printf("Usuario %d en estado: %s", userID, currentState)

	dsClient, err := deps.ensureAI()
	if err != nil {
		log.Printf("IA no disponible para usuario %d: %v", userID, err)
		if user.IsInChannel() {
			deps.handleConversation(w, user, audioData)
		} else {
			writeUnintelligibleResponse(w)
		}
		return
	}

	channels, err := userSvc.GetAvailableChannels()
	if err != nil {
		log.Printf("Error obteniendo canales para usuario %d: %v", userID, err)
		if user.IsInChannel() {
			deps.handleConversation(w, user, audioData)
		} else {
			writeUnintelligibleResponse(w)
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
			writeUnintelligibleResponse(w)
		}
		return
	}

	log.Printf("Resultado análisis usuario %d: comando=%v, intent=%s", userID, result.IsCommand, result.Intent)

	if result.IsCommand {
		svcPtr, _ := userSvc.(*services.UserService)
		cmdResponse, execErr := deps.executeCommand(user, svcPtr, result)
		if execErr != nil {
			log.Printf("Error ejecutando comando para usuario %d: %v", userID, execErr)
			http.Error(w, execErr.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(cmdResponse); err != nil {
			log.Printf("Error enviando respuesta JSON a usuario %d: %v", userID, err)
		}
		return
	}

	if !user.IsInChannel() {
		log.Printf("Usuario %d no está en canal, ignorando conversación", userID)
		writeUnintelligibleResponse(w)
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

func writeUnintelligibleResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(CommandResponse{
		Status:  "ignored",
		Intent:  "conversation",
		Message: "audio poco comprensible",
	})
}
