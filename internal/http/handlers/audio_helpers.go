package handlers

import (
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode"
	"walkie-backend/internal/config"

	"walkie-backend/internal/models"
	"walkie-backend/internal/services"
	"walkie-backend/pkg/qwen"
)

type AudioRelayResponse struct {
	Status      string  `json:"status"`
	Channel     string  `json:"channel"`
	Recipients  []uint  `json:"recipients"`
	AudioBase64 string  `json:"audioBase64"`
	Duration    float64 `json:"duration"`
	SampleRate  int     `json:"sampleRate"`
	Format      string  `json:"format"`
}

type CommandResponse struct {
	Status  string         `json:"status"`
	Intent  string         `json:"intent"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// executeCommand ejecuta un comando específico
func executeCommand(user *models.User, userService *services.UserService, result qwen.CommandResult) (CommandResponse, error) {
	switch result.Intent {
	case "request_channel_list":
		return handleChannelListCommand(userService)
	case "request_channel_connect":
		if len(result.Channels) == 0 {
			return CommandResponse{}, fmt.Errorf("no se especificó canal para conectar")
		}
		return handleChannelConnectCommand(user, userService, result.Channels[0])
	case "request_channel_disconnect":
		return handleChannelDisconnectCommand(user, userService)
	default:
		return CommandResponse{
			Status:  "ok",
			Intent:  result.Intent,
			Message: result.Reply,
		}, nil
	}
}

// handleChannelListCommand maneja el comando de listar canales
func handleChannelListCommand(userService *services.UserService) (CommandResponse, error) {
	channels, err := userService.GetAvailableChannels()
	if err != nil {
		return CommandResponse{}, fmt.Errorf("error obteniendo canales: %w", err)
	}

	channelNames := make([]string, 0, len(channels))
	channelCodes := make([]string, 0, len(channels))
	for _, ch := range channels {
		channelCodes = append(channelCodes, ch.Code)
		channelNames = append(channelNames, strings.TrimPrefix(ch.Code, "canal-"))
	}

	message := "No hay canales disponibles"
	if len(channelNames) > 0 {
		message = buildChannelListPhrase(channelNames)
	}

	return CommandResponse{
		Status:  "ok",
		Intent:  "request_channel_list",
		Message: message,
		Data: map[string]any{
			"channels":      channelCodes,
			"channel_names": channelNames,
		},
	}, nil
}

func buildChannelListPhrase(names []string) string {
	switch len(names) {
	case 0:
		return "No hay canales disponibles"
	case 1:
		return fmt.Sprintf("Canales disponibles: %s", names[0])
	case 2:
		return fmt.Sprintf("Canales disponibles: %s y %s", names[0], names[1])
	default:
		var sb strings.Builder
		sb.WriteString("Canales disponibles: ")
		last := len(names) - 1
		for i, name := range names {
			if i == last {
				sb.WriteString(fmt.Sprintf("y %s", name))
			} else {
				sb.WriteString(fmt.Sprintf("%s, ", name))
			}
		}
		return sb.String()
	}
}

// handleChannelConnectCommand maneja el comando de conectar a canal
func handleChannelConnectCommand(user *models.User, userService *services.UserService, channelCode string) (CommandResponse, error) {
	if err := userService.ConnectUserToChannel(user.ID, channelCode); err != nil {
		return CommandResponse{}, fmt.Errorf("no se pudo conectar al canal %s: %w", channelCode, err)
	}

	moveClientToChannel(user.ID, channelCode)
	channelNum := strings.TrimPrefix(channelCode, "canal-")

	return CommandResponse{
		Status:  "ok",
		Intent:  "request_channel_connect",
		Message: fmt.Sprintf("Conectado al canal %s", channelNum),
		Data: map[string]any{
			"channel":       channelCode,
			"channel_label": channelNum,
		},
	}, nil
}

// handleChannelDisconnectCommand maneja el comando de desconectar del canal
func handleChannelDisconnectCommand(user *models.User, userService *services.UserService) (CommandResponse, error) {
	if !user.IsInChannel() {
		return CommandResponse{
			Status:  "ok",
			Intent:  "request_channel_disconnect",
			Message: "No estás conectado a ningún canal",
		}, nil
	}

	currentChannel := user.GetCurrentChannelCode()
	if err := userService.DisconnectUserFromCurrentChannel(user.ID); err != nil {
		return CommandResponse{}, fmt.Errorf("no se pudo desconectar del canal: %w", err)
	}

	moveClientToChannel(user.ID, "")
	channelNum := strings.TrimPrefix(currentChannel, "canal-")

	return CommandResponse{
		Status:  "ok",
		Intent:  "request_channel_disconnect",
		Message: fmt.Sprintf("Desconectado del canal %s", channelNum),
		Data: map[string]any{
			"channel":       currentChannel,
			"channel_label": channelNum,
		},
	}, nil
}

// handleAsConversation maneja el audio como conversación
func handleAsConversation(w http.ResponseWriter, user *models.User, audioData []byte) {
	channelCode := user.GetCurrentChannelCode()
	if channelCode == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	log.Printf("Procesando audio de usuario %d en canal %s", user.ID, channelCode)

	startTransmission(channelCode, user.ID)
	broadcastAudio(channelCode, user.ID, audioData)

	duration := estimateAudioDuration(audioData)

	go func() {
		time.Sleep(duration)
		stopTransmission(channelCode, user.ID)
	}()

	userService := services.NewUserService()
	channelUsers, err := userService.GetChannelActiveUsers(channelCode)
	if err != nil {
		log.Printf("Error obteniendo usuarios del canal %s: %v", channelCode, err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	recipients := make([]uint, 0, len(channelUsers))
	for _, u := range channelUsers {
		if u.ID != user.ID {
			recipients = append(recipients, u.ID)
		}
	}

	EnqueueAudio(user.ID, channelCode, audioData, duration.Seconds(), recipients)

	// El emisor no recibe su propio audio; solo se responde 204.
	w.WriteHeader(http.StatusNoContent)
}

// --------------------------- helpers ---------------------------

func readUserIDHeader(r *http.Request) (uint, bool) {
	authToken := r.Header.Get("X-Auth-Token")
	if authToken == "" {
		return 0, false
	}

	// Buscar usuario por token
	var user models.User
	if err := config.DB.Where("auth_token = ?", authToken).First(&user).Error; err != nil {
		return 0, false
	}

	return user.ID, true
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
	dataSize := len(audioData)

	// Verificar y quitar header WAV
	if dataSize > 44 && string(audioData[:4]) == "RIFF" && string(audioData[8:12]) == "WAVE" {
		dataSize -= 44
	}

	// 16kHz * 2 bytes (16-bit) = 32000 bytes por segundo
	seconds := float64(dataSize) / 32000.0

	// Límites de seguridad
	if seconds < 0.5 {
		seconds = 0.5
	}
	if seconds > 30 {
		seconds = 30
	}

	return time.Duration(seconds * float64(time.Second))
}
