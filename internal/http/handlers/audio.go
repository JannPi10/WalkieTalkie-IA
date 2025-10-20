package handlers

import (
	"context"
	"fmt" // ← AGREGAR
	"log"
	"net/http"
	"strings"
	"time"

	"walkie-backend/internal/services"
)

// POST /audio/ingest
// Headers: X-User-ID: <uint>
// Body: audio/wav (raw) o multipart/form-data; name=file
func AudioIngest(w http.ResponseWriter, r *http.Request) {
	// Validación de método
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	// 1. Leer User-ID
	userID, ok := readUserIDHeader(r)
	if !ok {
		http.Error(w, "X-Auth-Token requerido", http.StatusBadRequest)
		return
	}

	// 2. Crear contexto con timeout
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	// 3. Leer audio del request
	audioData, err := readAudioFromRequest(r)
	if err != nil || len(audioData) == 0 {
		log.Printf("Error leyendo audio de usuario %d: %v", userID, err)
		http.Error(w, "Audio requerido", http.StatusBadRequest)
		return
	}

	log.Printf("Audio recibido de usuario %d, tamaño: %d bytes", userID, len(audioData))

	// 4. Validar formato WAV
	if !isValidWAVFormat(audioData) {
		log.Printf("Formato de audio inválido de usuario %d", userID)
		http.Error(w, "Formato de audio inválido. Se requiere WAV", http.StatusBadRequest)
		return
	}

	// 5. Obtener usuario con su canal actual
	userService := services.NewUserService()
	user, err := userService.GetUserWithChannel(userID)
	if err != nil {
		log.Printf("Usuario %d no encontrado: %v", userID, err)
		http.Error(w, "Usuario no encontrado", http.StatusNotFound)
		return
	}

	// 6. STT - Convertir audio a texto
	sttClient, err := EnsureSTTClient()
	if err != nil {
		log.Printf("STT no disponible para usuario %d: %v", userID, err)
		http.Error(w, "Servicio de transcripción no disponible", http.StatusServiceUnavailable)
		return
	}

	text, err := sttClient.TranscribeAudio(ctx, audioData)
	if err != nil {
		log.Printf("Error en STT para usuario %d: %v", userID, err)
		// Si falla STT y está en canal, enviar como conversación sin análisis
		if user.IsInChannel() {
			log.Printf("Enviando audio sin transcripción para usuario %d (en canal)", userID)
			handleAsConversation(w, user, audioData)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	text = strings.TrimSpace(text)
	log.Printf("Texto transcrito de usuario %d: '%s'", userID, text)

	// 7. Verificar coherencia del texto
	if !isLikelyCoherent(text) {
		log.Printf("Texto no coherente de usuario %d, ignorando", userID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 8. Determinar estado del usuario
	currentState := "sin_canal"
	if user.IsInChannel() {
		currentState = user.GetCurrentChannelCode()
	}

	log.Printf("Usuario %d en estado: %s", userID, currentState)

	// 9. Analizar intención con IA
	dsClient, err := EnsureDeepseekClient()
	if err != nil {
		log.Printf("Deepseek no disponible para usuario %d: %v", userID, err)
		// Fallback: si está en canal, enviar como conversación
		if user.IsInChannel() {
			handleAsConversation(w, user, audioData)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	// 10. Obtener canales disponibles
	channels, err := userService.GetAvailableChannels()
	if err != nil {
		log.Printf("Error obteniendo canales para usuario %d: %v", userID, err)
		// Fallback: si está en canal, enviar como conversación
		if user.IsInChannel() {
			handleAsConversation(w, user, audioData)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	chanCodes := make([]string, 0, len(channels))
	for _, ch := range channels {
		chanCodes = append(chanCodes, ch.Code)
	}

	// 11. Analizar con IA
	result, err := dsClient.AnalyzeTranscript(ctx, text, chanCodes, currentState, "")
	if err != nil {
		log.Printf("Error analizando transcripción para usuario %d: %v", userID, err)
		// Fallback inteligente: si está en canal, tratar como conversación
		if user.IsInChannel() {
			log.Printf("Fallback: tratando como conversación para usuario %d", userID)
			handleAsConversation(w, user, audioData)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	log.Printf("Resultado análisis usuario %d: comando=%v, intent=%s", userID, result.IsCommand, result.Intent)

	// 12. Procesar resultado
	if result.IsCommand {
		response, execErr := executeCommand(user, userService, result)
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

	// 13. No es comando - manejar como conversación
	if !user.IsInChannel() {
		log.Printf("Usuario %d no está en canal, ignorando conversación", userID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	handleAsConversation(w, user, audioData)
}

// GET /audio/poll
// Headers: X-Auth-Token: <token>
// Endpoint para que los clientes obtengan audio pendiente mediante polling
func AudioPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	// Leer userId desde token
	userID, ok := readUserIDHeader(r)
	if !ok {
		http.Error(w, "X-Auth-Token requerido", http.StatusUnauthorized)
		return
	}

	// Verificar si hay audio pendiente
	pendingAudio := DequeueAudio(userID)

	if pendingAudio != nil {
		// Devolver audio como WAV
		log.Printf("Usuario %d recibe audio pendiente de usuario %d via polling", userID, pendingAudio.SenderID)

		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("X-Audio-From", fmt.Sprintf("%d", pendingAudio.SenderID))
		w.Header().Set("X-Channel", pendingAudio.Channel)
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(pendingAudio.AudioData)
		if err != nil {
			log.Printf("Error enviando audio a usuario %d: %v", userID, err)
		}
		return
	}

	// No hay audio pendiente
	w.WriteHeader(http.StatusNoContent)
}
