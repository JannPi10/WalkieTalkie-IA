package handlers

import (
	"encoding/binary"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/pkg/deepseek"
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
		http.Error(w, "Audio requerido", http.StatusBadRequest)
		return
	}

	// 2) Obtener usuario
	var user models.User
	if err := config.DB.First(&user, userID).Error; err != nil {
		http.Error(w, "Usuario no encontrado", http.StatusNotFound)
		return
	}

	// 3) STT
	sttClient, err := EnsureSTTClient()
	if err != nil {
		http.Error(w, "STT no disponible", http.StatusServiceUnavailable)
		return
	}
	text, err := sttClient.TranscribeAudio(r.Context(), audioData)
	if err != nil {
		// Si no pudimos transcribir, no tenemos forma confiable de saber si es conversación.
		// Ignoramos (204).
		w.WriteHeader(http.StatusNoContent)
		return
	}
	text = strings.TrimSpace(text)

	// 4) Coherencia mínima del texto (evitar ruido / respiraciones)
	//    - Si no es coherente: ignoramos (204).
	if !isLikelyCoherent(text) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 5) Analizar intención (comando vs conversación)
	dsClient, err := EnsureDeepseekClient()
	if err != nil {
		// IA offline → tratamos como conversación si está en canal
		handleAsConversationOrIgnore(w, &user, audioData)
		return
	}

	// Canales disponibles (códigos)
	var channels []models.Channel
	if err := config.DB.Where("is_private = ?", false).Find(&channels).Error; err != nil {
		// Si no puedo listar canales, igual intento tratar como conversación
		handleAsConversationOrIgnore(w, &user, audioData)
		return
	}
	chanCodes := make([]string, 0, len(channels))
	for _, ch := range channels {
		chanCodes = append(chanCodes, ch.Code)
	}

	state := "sin_canal"
	if user.CurrentChannelID != nil {
		state = "en_canal"
	}

	result, err := dsClient.AnalyzeTranscript(r.Context(), text, chanCodes, state, "")
	if err != nil {
		http.Error(w, "No se pudo analizar el audio", http.StatusBadGateway)
		return
	}

	// 6) Si es comando → ejecutar y responder texto (200)
	if result.IsCommand {
		msg, execErr := executeCommand(&user, currentChannelOf(&user), result)
		if execErr != nil {
			http.Error(w, execErr.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(msg))
		return
	}

	// 7) No es comando → conversación:
	//    - Si NO está en canal: ignorar (204)
	//    - Si SÍ está en canal: STOP → audio → START (con duración estimada)
	handleAsConversationOrIgnore(w, &user, audioData)
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
	// Heurística sencilla:
	// - Al menos 6 letras
	// - Al menos 1 vocal
	// - Al menos 2 “palabras” con letras (>=2 chars)
	letters := 0
	vowels := 0
	wordCount := 0

	for _, w := range strings.Fields(s) {
		alpha := 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
				alpha++
				switch unicode.ToLower(r) {
				case 'a', 'e', 'i', 'o', 'u', 'á', 'é', 'í', 'ó', 'ú':
					vowels++
				}
			}
		}
		if alpha >= 2 {
			wordCount++
		}
	}
	return letters >= 6 && vowels >= 1 && wordCount >= 2
}

func currentChannelOf(user *models.User) *models.Channel {
	if user.CurrentChannelID == nil {
		return nil
	}
	var ch models.Channel
	if err := config.DB.First(&ch, *user.CurrentChannelID).Error; err != nil {
		return nil
	}
	return &ch
}

func handleAsConversationOrIgnore(w http.ResponseWriter, user *models.User, audio []byte) {
	ch := currentChannelOf(user)
	if ch == nil {
		// No hay canal → ignorar audio normal
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Señalización y difusión del audio
	startTransmission(ch.Code, user.ID)
	broadcastAudio(ch.Code, user.ID, audio)

	// Calcula duración aproximada del WAV (si aplica) para mandar START después
	dur := estimateWavDuration(audio)
	if dur <= 0 {
		dur = 1500 * time.Millisecond
	} else {
		// pequeño margen por red/decodificación
		dur += 100 * time.Millisecond
	}

	time.AfterFunc(dur, func() {
		stopTransmission(ch.Code, user.ID)
	})

	// No respondemos texto si es conversación
	w.WriteHeader(http.StatusNoContent)
}

// estimateWavDuration intenta leer la duración de un WAV PCM 16-bit.
func estimateWavDuration(b []byte) time.Duration {
	// Formato RIFF WAVE, cabecera mínima 44 bytes
	if len(b) < 44 {
		return 0
	}
	if string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0
	}
	// ByteRate en offset 28 (little-endian, uint32)
	byteRate := binary.LittleEndian.Uint32(b[28:32])
	// Data chunk típicamente a partir de 44; tomamos el tamaño total menos 44
	if byteRate == 0 {
		return 0
	}
	dataBytes := len(b) - 44
	secs := float64(dataBytes) / float64(byteRate)
	if secs <= 0 || secs > 600 { // cap 10min por seguridad
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// ----------------- comandos (usa deepseek.CommandResult) -----------------

func executeCommand(user *models.User, currentChannel *models.Channel, result deepseek.CommandResult) (string, error) {
	switch result.Intent {
	case "request_channel_list", "confirm_channel_list":
		return listChannelsReply(), nil

	case "request_channel_connect", "confirm_channel_connect":
		if len(result.Channels) == 0 {
			return "", fmt.Errorf("No se detectó canal destino")
		}
		code := result.Channels[0]
		var channel models.Channel
		if err := config.DB.Where("code = ?", code).First(&channel).Error; err != nil {
			return "", fmt.Errorf("Canal %s no existe", code)
		}
		if err := moveUserToChannel(user, &channel); err != nil {
			return "", err
		}
		return fmt.Sprintf("Listo, te conecté al %s", channel.Code), nil

	case "request_channel_disconnect", "confirm_channel_disconnect":
		if currentChannel == nil {
			return "No estás en ningún canal.", nil
		}
		if err := moveUserToChannel(user, nil); err != nil {
			return "", err
		}
		return "Saliste del canal, dime a cuál quieres unirte.", nil

	default:
		// Intento no reconocido → devuelve la transcripción como reply
		return "No entendí el comando, ¿podrías repetirlo?", nil
	}
}

func listChannelsReply() string {
	var channels []models.Channel
	_ = config.DB.Where("is_private = ?", false).Find(&channels).Error
	names := make([]string, 0, len(channels))
	for _, ch := range channels {
		names = append(names, ch.Code)
	}
	return "Los canales disponibles son: " + strings.Join(names, ", ")
}

func moveUserToChannel(user *models.User, channel *models.Channel) error {
	tx := config.DB.Begin()

	var newChannelID *uint
	if channel != nil {
		newChannelID = &channel.ID
	}

	if err := tx.Model(user).Updates(map[string]any{
		"current_channel_id": newChannelID,
		"last_active_at":     time.Now(),
	}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("No se pudo actualizar al usuario")
	}

	if channel != nil {
		membership := models.ChannelMembership{
			UserID:    user.ID,
			ChannelID: channel.ID,
			Active:    true,
			JoinedAt:  time.Now(),
		}
		if err := tx.Where("user_id = ? AND channel_id = ?", user.ID, channel.ID).
			Assign(membership).
			FirstOrCreate(&membership).Error; err != nil {
			tx.Rollback()
			return fmt.Errorf("No se pudo registrar la membresía")
		}
		tx.Model(&models.ChannelMembership{}).
			Where("user_id = ? AND channel_id <> ?", user.ID, channel.ID).
			Updates(map[string]any{"active": false, "left_at": time.Now()})
	} else {
		tx.Model(&models.ChannelMembership{}).
			Where("user_id = ?", user.ID).
			Updates(map[string]any{"active": false, "left_at": time.Now()})
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("No se pudo guardar el cambio de canal")
	}

	// Actualizar el registro WS si existe
	if channel != nil {
		moveClientToChannel(user.ID, channel.Code)
	} else {
		moveClientToChannel(user.ID, "")
	}
	return nil
}
