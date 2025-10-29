package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
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

type stageTimer struct {
	userID uint
	start  time.Time
}

func newStageTimer(userID uint) *stageTimer {
	return &stageTimer{
		userID: userID,
		start:  time.Now(),
	}
}

func (t *stageTimer) LogStage(stage string, stageStart time.Time, attrs map[string]any) {
	duration := time.Since(stageStart)
	total := time.Since(t.start)

	line := fmt.Sprintf("[TIMING] user=%d stage=%s duration_ms=%.2f total_ms=%.2f",
		t.userID,
		stage,
		float64(duration)/float64(time.Millisecond),
		float64(total)/float64(time.Millisecond),
	)

	if len(attrs) > 0 {
		keys := make([]string, 0, len(attrs))
		for k := range attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, attrs[k]))
		}
		line += " " + strings.Join(parts, " ")
	}

	log.Print(line)
}

func (t *stageTimer) LogFinal(reason string) {
	log.Printf("[TIMING] user=%d stage=finished total_ms=%.2f (%s)",
		t.userID,
		float64(time.Since(t.start))/float64(time.Millisecond),
		reason,
	)
}

// POST /audio/ingest
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

	tracker := newStageTimer(userID)

	audioData, ok := readAndValidateAudio(w, r, deps, userID, tracker)
	if !ok {
		return
	}

	user, userSvc, ok := loadUserContext(w, deps, userID, tracker)
	if !ok {
		return
	}

	sttClient, ok := ensureSTTClientStage(w, deps, userID, tracker)
	if !ok {
		return
	}

	text, ok := transcribeAudioStage(ctx, w, sttClient, user, audioData, deps, tracker)
	if !ok {
		return
	}

	if !checkCoherenceStage(w, deps, user, text, tracker) {
		return
	}

	currentState := "sin_canal"
	if user.IsInChannel() {
		currentState = user.GetCurrentChannelCode()
	}
	log.Printf("Usuario %d en estado: %s", user.ID, currentState)

	aiClient, ok := ensureAIClientStage(w, deps, user, audioData, tracker)
	if !ok {
		return
	}

	channelCodes, ok := loadChannelCodesStage(w, userSvc, deps, user, audioData, tracker)
	if !ok {
		return
	}

	result, ok := analyzeTranscriptStage(ctx, w, aiClient, text, channelCodes, currentState, deps, user, audioData, tracker)
	if !ok {
		return
	}

	log.Printf("Resultado análisis usuario %d: comando=%v, intent=%s", user.ID, result.IsCommand, result.Intent)

	if result.IsCommand {
		if handleCommandStage(w, user, userSvc, result, deps, tracker) {
			return
		}
	}

	if !user.IsInChannel() {
		log.Printf("Usuario %d no está en canal, ignorando conversación", user.ID)
		writeUnintelligibleResponse(w)
		tracker.LogFinal("no_channel")
		return
	}

	if handleConversationStage(w, user, audioData, deps, tracker) {
		return
	}
}

func readAndValidateAudio(w http.ResponseWriter, r *http.Request, deps audioIngestDeps, userID uint, tracker *stageTimer) ([]byte, bool) {
	stageStart := time.Now()
	audioData, err := deps.readAudio(r)
	if err != nil || len(audioData) == 0 {
		log.Printf("Error leyendo audio de usuario %d: %v", userID, err)
		http.Error(w, "Audio requerido", http.StatusBadRequest)
		tracker.LogFinal("audio_read_error")
		return nil, false
	}

	tracker.LogStage("received", stageStart, map[string]any{
		"size_bytes": len(audioData),
	})

	if !deps.validateWAV(audioData) {
		log.Printf("Formato de audio inválido de usuario %d", userID)
		http.Error(w, "Formato de audio inválido. Se requiere WAV", http.StatusBadRequest)
		tracker.LogFinal("invalid_wav")
		return nil, false
	}

	return audioData, true
}

func loadUserContext(w http.ResponseWriter, deps audioIngestDeps, userID uint, tracker *stageTimer) (*models.User, *services.UserService, bool) {
	svcIface := deps.newUserService()
	if svcIface == nil {
		http.Error(w, "Servicio de usuarios no disponible", http.StatusInternalServerError)
		tracker.LogFinal("user_service_nil")
		return nil, nil, false
	}

	svc, ok := svcIface.(*services.UserService)
	if !ok {
		http.Error(w, "Servicio de usuarios inválido", http.StatusInternalServerError)
		tracker.LogFinal("user_service_type")
		return nil, nil, false
	}

	stageStart := time.Now()
	user, err := svc.GetUserWithChannel(userID)
	tracker.LogStage("load_user", stageStart, nil)

	if err != nil {
		log.Printf("Usuario %d no encontrado: %v", userID, err)
		http.Error(w, "Usuario no encontrado", http.StatusNotFound)
		tracker.LogFinal("user_not_found")
		return nil, nil, false
	}

	return user, svc, true
}

func ensureSTTClientStage(w http.ResponseWriter, deps audioIngestDeps, userID uint, tracker *stageTimer) (sttClient, bool) {
	stageStart := time.Now()
	client, err := deps.ensureSTT()
	tracker.LogStage("ensure_stt", stageStart, nil)

	if err != nil {
		log.Printf("STT no disponible para usuario %d: %v", userID, err)
		http.Error(w, "Servicio de transcripción no disponible", http.StatusServiceUnavailable)
		tracker.LogFinal("stt_unavailable")
		return nil, false
	}

	return client, true
}

func transcribeAudioStage(ctx context.Context, w http.ResponseWriter, stt sttClient, user *models.User, audio []byte, deps audioIngestDeps, tracker *stageTimer) (string, bool) {
	stageStart := time.Now()
	text, err := stt.TranscribeAudio(ctx, audio)
	text = strings.TrimSpace(text)
	tracker.LogStage("stt", stageStart, map[string]any{
		"text_len": len(text),
	})

	if err != nil {
		log.Printf("Error en STT para usuario %d: %v", user.ID, err)
		if user.IsInChannel() {
			log.Printf("Enviando audio sin transcripción para usuario %d (en canal)", user.ID)
			deps.handleConversation(w, user, audio)
		} else {
			writeUnintelligibleResponse(w)
		}
		tracker.LogFinal("stt_error")
		return "", false
	}

	return text, true
}

func checkCoherenceStage(w http.ResponseWriter, deps audioIngestDeps, user *models.User, text string, tracker *stageTimer) bool {
	stageStart := time.Now()
	coherent := deps.isCoherent(text)
	tracker.LogStage("coherence", stageStart, map[string]any{
		"coherent": coherent,
	})

	if coherent {
		return true
	}

	log.Printf("Texto no coherente de usuario %d, ignorando", user.ID)
	if user.IsInChannel() {
		w.WriteHeader(http.StatusNoContent)
	} else {
		writeUnintelligibleResponse(w)
	}
	tracker.LogFinal("incoherent")
	return false
}

func ensureAIClientStage(w http.ResponseWriter, deps audioIngestDeps, user *models.User, audio []byte, tracker *stageTimer) (qwenClient, bool) {
	stageStart := time.Now()
	client, err := deps.ensureAI()
	tracker.LogStage("ensure_ai", stageStart, nil)

	if err != nil {
		log.Printf("IA no disponible para usuario %d: %v", user.ID, err)
		if user.IsInChannel() {
			deps.handleConversation(w, user, audio)
		} else {
			writeUnintelligibleResponse(w)
		}
		tracker.LogFinal("ai_unavailable")
		return nil, false
	}

	return client, true
}

func loadChannelCodesStage(w http.ResponseWriter, svc *services.UserService, deps audioIngestDeps, user *models.User, audio []byte, tracker *stageTimer) ([]string, bool) {
	stageStart := time.Now()
	channels, err := svc.GetAvailableChannels()
	tracker.LogStage("list_channels", stageStart, map[string]any{
		"count": len(channels),
	})

	if err != nil {
		log.Printf("Error obteniendo canales para usuario %d: %v", user.ID, err)
		if user.IsInChannel() {
			deps.handleConversation(w, user, audio)
		} else {
			writeUnintelligibleResponse(w)
		}
		tracker.LogFinal("channels_error")
		return nil, false
	}

	codes := make([]string, len(channels))
	for i, ch := range channels {
		codes[i] = ch.Code
	}

	return codes, true
}

func analyzeTranscriptStage(ctx context.Context, w http.ResponseWriter, ai qwenClient, text string, channels []string, state string, deps audioIngestDeps, user *models.User, audio []byte, tracker *stageTimer) (qwen.CommandResult, bool) {
	stageStart := time.Now()
	result, err := ai.AnalyzeTranscript(ctx, text, channels, state, "")
	tracker.LogStage("ai", stageStart, map[string]any{
		"intent":     result.Intent,
		"is_command": result.IsCommand,
	})

	if err != nil {
		log.Printf("Error analizando transcripción para usuario %d: %v", user.ID, err)
		if user.IsInChannel() {
			log.Printf("Fallback: tratando como conversación para usuario %d", user.ID)
			deps.handleConversation(w, user, audio)
		} else {
			writeUnintelligibleResponse(w)
		}
		tracker.LogFinal("ai_error")
		return qwen.CommandResult{}, false
	}

	return result, true
}

func handleCommandStage(w http.ResponseWriter, user *models.User, svc *services.UserService, result qwen.CommandResult, deps audioIngestDeps, tracker *stageTimer) bool {
	stageStart := time.Now()
	cmdResponse, err := deps.executeCommand(user, svc, result)
	tracker.LogStage("execute_command", stageStart, map[string]any{
		"intent": result.Intent,
		"error":  err != nil,
	})

	if err != nil {
		log.Printf("Error ejecutando comando para usuario %d: %v", user.ID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		tracker.LogFinal("command_error")
		return true
	}

	stageStart = time.Now()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if encodeErr := json.NewEncoder(w).Encode(cmdResponse); encodeErr != nil {
		log.Printf("Error enviando respuesta JSON a usuario %d: %v", user.ID, encodeErr)
	}
	tracker.LogStage("response", stageStart, map[string]any{
		"intent": result.Intent,
	})

	tracker.LogFinal("command_response")
	return true
}

func handleConversationStage(w http.ResponseWriter, user *models.User, audio []byte, deps audioIngestDeps, tracker *stageTimer) bool {
	stageStart := time.Now()
	deps.handleConversation(w, user, audio)
	tracker.LogStage("broadcast", stageStart, nil)
	tracker.LogFinal("broadcast_done")
	return true
}

// GET /audio/poll
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
