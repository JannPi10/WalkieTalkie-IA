package handlers

import (
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"walkie-backend/internal/models"
	"walkie-backend/internal/services"
	"walkie-backend/pkg/deepseek"
)

// AudioRelayResponse es la respuesta cuando el audio se envía a otros usuarios
type AudioRelayResponse struct {
	Status      string  `json:"status"`
	Channel     string  `json:"channel"`
	Recipients  []uint  `json:"recipients"`
	AudioBase64 string  `json:"audioBase64"`
	Duration    float64 `json:"duration"`
	SampleRate  int     `json:"sampleRate"`
	Format      string  `json:"format"`
}

// executeCommand ejecuta un comando específico
func executeCommand(user *models.User, userService *services.UserService, result deepseek.CommandResult) (string, error) {
	switch result.Intent {
	case "request_channel_list":
		return handleChannelListCommand(userService)

	case "request_channel_connect":
		if len(result.Channels) == 0 {
			return "", fmt.Errorf("no se especificó canal para conectar")
		}
		return handleChannelConnectCommand(user, userService, result.Channels[0])

	case "request_channel_disconnect":
		return handleChannelDisconnectCommand(user, userService)

	default:
		return result.Reply, nil
	}
}

// handleChannelListCommand maneja el comando de listar canales
func handleChannelListCommand(userService *services.UserService) (string, error) {
	channels, err := userService.GetAvailableChannels()
	if err != nil {
		return "", fmt.Errorf("error obteniendo canales: %w", err)
	}

	if len(channels) == 0 {
		return "No hay canales disponibles", nil
	}

	// Crear lista más natural para voz
	var response strings.Builder
	response.WriteString("Canales disponibles: ")

	channelNames := make([]string, 0, len(channels))
	for _, ch := range channels {
		// Extraer solo el número del código (canal-1 -> 1)
		channelNum := strings.TrimPrefix(ch.Code, "canal-")
		channelNames = append(channelNames, channelNum)
	}

	// Unir con comas: "1, 2, 3, 4 y 5"
	if len(channelNames) == 1 {
		response.WriteString(channelNames[0])
	} else if len(channelNames) == 2 {
		response.WriteString(fmt.Sprintf("%s y %s", channelNames[0], channelNames[1]))
	} else {
		lastIdx := len(channelNames) - 1
		for i, name := range channelNames {
			if i == lastIdx {
				response.WriteString(fmt.Sprintf("y %s", name))
			} else {
				response.WriteString(fmt.Sprintf("%s, ", name))
			}
		}
	}

	return response.String(), nil
}

// handleChannelConnectCommand maneja el comando de conectar a canal
func handleChannelConnectCommand(user *models.User, userService *services.UserService, channelCode string) (string, error) {
	if err := userService.ConnectUserToChannel(user.ID, channelCode); err != nil {
		return "", fmt.Errorf("no se pudo conectar al canal %s: %w", channelCode, err)
	}

	// Notificar via WebSocket el cambio de canal
	moveClientToChannel(user.ID, channelCode)

	// Extraer número del canal para respuesta más natural
	channelNum := strings.TrimPrefix(channelCode, "canal-")
	return fmt.Sprintf("Conectado al canal %s", channelNum), nil
}

// handleChannelDisconnectCommand maneja el comando de desconectar del canal
func handleChannelDisconnectCommand(user *models.User, userService *services.UserService) (string, error) {
	if !user.IsInChannel() {
		return "No estás conectado a ningún canal", nil
	}

	currentChannel := user.GetCurrentChannelCode()
	if err := userService.DisconnectUserFromCurrentChannel(user.ID); err != nil {
		return "", fmt.Errorf("no se pudo desconectar del canal: %w", err)
	}

	// Notificar via WebSocket la desconexión
	moveClientToChannel(user.ID, "")

	// Extraer número del canal para respuesta más natural
	channelNum := strings.TrimPrefix(currentChannel, "canal-")
	return fmt.Sprintf("Desconectado del canal %s", channelNum), nil
}

// handleAsConversation maneja el audio como conversación
func handleAsConversation(w http.ResponseWriter, user *models.User, audioData []byte) {
	channelCode := user.GetCurrentChannelCode()
	if channelCode == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	log.Printf("Procesando audio de usuario %d en canal %s", user.ID, channelCode)

	// 1. Enviar señales STOP/START via WebSocket (si hay)
	startTransmission(channelCode, user.ID)

	// 2. Enviar audio via WebSocket (si hay)
	broadcastAudio(channelCode, user.ID, audioData)

	// 3. Estimar duración del audio
	duration := estimateAudioDuration(audioData)

	// 4. Programar detención de transmisión
	go func() {
		time.Sleep(duration)
		stopTransmission(channelCode, user.ID)
	}()

	// 5. Obtener usuarios del canal desde BASE DE DATOS
	userService := services.NewUserService()
	channelUsers, err := userService.GetChannelActiveUsers(channelCode)
	if err != nil {
		log.Printf("Error obteniendo usuarios del canal %s: %v", channelCode, err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Crear lista de recipients (excluir al sender)
	recipients := make([]uint, 0, len(channelUsers))
	for _, u := range channelUsers {
		if u.ID != user.ID {
			recipients = append(recipients, u.ID)
		}
	}

	// 6. GUARDAR audio en cola para otros usuarios
	EnqueueAudio(user.ID, channelCode, audioData, duration.Seconds(), recipients)

	// 7. VERIFICAR si este usuario tiene audio pendiente
	pendingAudio := DequeueAudio(user.ID)

	if pendingAudio != nil {
		// Usuario tiene audio pendiente, devolverlo como WAV binario
		log.Printf("Usuario %d recibe audio pendiente de usuario %d", user.ID, pendingAudio.SenderID)

		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("X-Audio-From", fmt.Sprintf("%d", pendingAudio.SenderID))
		w.Header().Set("X-Channel", pendingAudio.Channel)
		w.WriteHeader(http.StatusOK)
		_, err := w.Write(pendingAudio.AudioData)
		if err != nil {
			log.Printf("Error enviando audio a usuario %d: %v", user.ID, err)
		}
		return
	}

	// 8. No hay audio pendiente, responder 204 No Content
	log.Printf("Audio procesado: usuario=%d, canal=%s, destinatarios=%d, sin_audio_pendiente",
		user.ID, channelCode, len(recipients))
	w.WriteHeader(http.StatusNoContent)
}

// --------------------------- helpers ---------------------------

func readUserIDHeader(r *http.Request) (uint, bool) {
	if v := r.Header.Get("X-User-ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			return uint(n), true
		}
	}
	return 0, false
}

func readAudioFromRequest(r *http.Request) ([]byte, error) {
	ct := r.Header.Get("Content-Type")
	mt, params, _ := mime.ParseMediaType(ct)

	// multipart/form-data -> campo "file"
	if strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			return nil, err
		}
		defer part.Close()
		return io.ReadAll(io.LimitReader(part, 20<<20)) // 20 MB max
	}

	// raw audio/*
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, 20<<20))
}

// isValidWAVFormat valida que el audio sea formato WAV válido
func isValidWAVFormat(data []byte) bool {
	if len(data) < 44 {
		return false
	}
	// Verificar header RIFF y WAVE
	return string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

func isLikelyCoherent(s string) bool {
	// Heurística mejorada para detectar habla coherente
	s = strings.TrimSpace(s)

	// Aceptar frases muy cortas comunes
	if len(s) <= 5 {
		common := []string{"si", "sí", "no", "ok", "vale", "bien"}
		lower := strings.ToLower(s)
		for _, word := range common {
			if lower == word {
				return true
			}
		}
	}

	if len(s) < 3 {
		return false
	}

	letters := 0
	vowels := 0
	wordCount := 0

	for _, w := range strings.Fields(s) {
		alpha := 0
		hasVowel := false
		for _, r := range w {
			if unicode.IsLetter(r) {
				alpha++
				letters++
				if strings.ContainsRune("aeiouáéíóúAEIOUÁÉÍÓÚ", r) {
					vowels++
					hasVowel = true
				}
			}
		}
		if alpha >= 1 && hasVowel {
			wordCount++
		}
	}

	// Criterios más flexibles
	return letters >= 3 && vowels >= 1 && wordCount >= 1
}

func estimateAudioDuration(audioData []byte) time.Duration {
	// Estimación para WAV 16kHz, 16-bit, mono
	dataSize := len(audioData)

	// Verificar y quitar header WAV
	if dataSize > 44 && string(audioData[:4]) == "RIFF" && string(audioData[8:12]) == "WAVE" {
		dataSize -= 44
	}

	// 16kHz * 2 bytes (16-bit) = 32000 bytes por segundo
	seconds := float64(dataSize) / 32000.0

	// Límites de seguridad
	if seconds < 0.5 {
		seconds = 0.5 // Mínimo 500ms
	}
	if seconds > 30 {
		seconds = 30 // Máximo 30s
	}

	return time.Duration(seconds * float64(time.Second))
}
