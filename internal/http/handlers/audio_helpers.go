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

	var response strings.Builder
	response.WriteString("Canales disponibles:\n")
	for _, ch := range channels {
		response.WriteString(fmt.Sprintf("- %s: %s\n", ch.Code, ch.Name))
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

	return fmt.Sprintf("Conectado al canal %s", channelCode), nil
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

	return fmt.Sprintf("Desconectado del canal %s", currentChannel), nil
}

// handleAsConversation maneja el audio como conversación
func handleAsConversation(w http.ResponseWriter, user *models.User, audioData []byte) {
	channelCode := user.GetCurrentChannelCode()
	if channelCode == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	log.Printf("Enviando audio de usuario %d a canal %s", user.ID, channelCode)

	// Enviar señales STOP/START y audio via WebSocket
	startTransmission(channelCode, user.ID)

	// Enviar audio a otros usuarios del canal
	broadcastAudio(channelCode, user.ID, audioData)

	// Estimar duración del audio y programar STOP
	duration := estimateAudioDuration(audioData)
	go func() {
		time.Sleep(duration)
		stopTransmission(channelCode, user.ID)
	}()

	// Responder con 204 No Content (no hay texto que devolver)
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
		return io.ReadAll(io.LimitReader(part, 20<<20)) // 20 MB
	}

	// raw audio/*
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, 20<<20))
}

func isLikelyCoherent(s string) bool {
	// Heurística mejorada para detectar habla coherente
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
		if alpha >= 2 && hasVowel {
			wordCount++
		}
	}

	// Criterios más estrictos
	return letters >= 6 && vowels >= 2 && wordCount >= 2
}

func estimateAudioDuration(audioData []byte) time.Duration {
	// Estimación básica: asumiendo WAV 16kHz, 16-bit, mono
	// 44 bytes de header WAV típico
	dataSize := len(audioData)
	if dataSize > 44 && string(audioData[:4]) == "RIFF" {
		dataSize -= 44 // Quitar header WAV
	}

	// 16kHz * 2 bytes = 32000 bytes por segundo
	seconds := float64(dataSize) / 32000.0
	if seconds < 0.5 {
		seconds = 0.5 // Mínimo 500ms
	}
	if seconds > 30 {
		seconds = 30 // Máximo 30s
	}

	return time.Duration(seconds * float64(time.Second))
}
