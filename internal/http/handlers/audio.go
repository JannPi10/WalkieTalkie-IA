package handlers

import (
	"log"
	"net/http"
	"strings"
	"walkie-backend/internal/services"
)

// POST /audio/ingest
// Headers: X-User-ID: <uint>
// Body: audio/wav (raw) o multipart/form-data; name=file
func AudioIngest(w http.ResponseWriter, r *http.Request) {
	userID, ok := readUserIDHeader(r)
	if !ok {
		http.Error(w, "X-User-ID requerido", http.StatusBadRequest)
		return
	}

	// 1) Leer audio (raw o multipart)
	audioData, err := readAudioFromRequest(r)
	if err != nil || len(audioData) == 0 {
		log.Printf("Error leyendo audio de usuario %d: %v", userID, err)
		http.Error(w, "Audio requerido", http.StatusBadRequest)
		return
	}

	log.Printf("Audio recibido de usuario %d, tamaño: %d bytes", userID, len(audioData))

	// 2) Obtener usuario con su canal actual
	userService := services.NewUserService()
	user, err := userService.GetUserWithChannel(userID)
	if err != nil {
		log.Printf("Usuario %d no encontrado: %v", userID, err)
		http.Error(w, "Usuario no encontrado", http.StatusNotFound)
		return
	}

	// 3) STT - Convertir audio a texto
	sttClient, err := EnsureSTTClient()
	if err != nil {
		log.Printf("STT no disponible para usuario %d: %v", userID, err)
		http.Error(w, "Servicio de transcripción no disponible", http.StatusServiceUnavailable)
		return
	}

	text, err := sttClient.TranscribeAudio(r.Context(), audioData)
	if err != nil {
		log.Printf("Error en STT para usuario %d: %v", userID, err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	text = strings.TrimSpace(text)
	log.Printf("Texto transcrito de usuario %d: '%s'", userID, text)

	// 4) Verificar coherencia del texto
	if !isLikelyCoherent(text) {
		log.Printf("Texto no coherente de usuario %d, ignorando", userID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 5) Determinar estado del usuario
	currentState := "sin_canal"
	if user.IsInChannel() {
		currentState = user.GetCurrentChannelCode()
	}

	log.Printf("Usuario %d en estado: %s", userID, currentState)

	// 6) Analizar intención con IA
	dsClient, err := EnsureDeepseekClient()
	if err != nil {
		log.Printf("Deepseek no disponible para usuario %d: %v", userID, err)
		if user.IsInChannel() {
			handleAsConversation(w, user, audioData)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	// Obtener canales disponibles
	channels, err := userService.GetAvailableChannels()
	if err != nil {
		log.Printf("Error obteniendo canales para usuario %d: %v", userID, err)
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

	// Analizar con IA
	result, err := dsClient.AnalyzeTranscript(r.Context(), text, chanCodes, currentState, "")
	if err != nil {
		log.Printf("Error analizando transcripción para usuario %d: %v", userID, err)
		http.Error(w, "No se pudo analizar el audio", http.StatusBadGateway)
		return
	}

	log.Printf("Resultado análisis usuario %d: comando=%v, intent=%s", userID, result.IsCommand, result.Intent)

	// 7) Procesar resultado
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

	// 8) No es comando - manejar como conversación
	if !user.IsInChannel() {
		log.Printf("Usuario %d no está en canal, ignorando conversación", userID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	handleAsConversation(w, user, audioData)
}
