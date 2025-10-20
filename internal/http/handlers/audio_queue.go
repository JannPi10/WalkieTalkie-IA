package handlers

import (
	"encoding/base64"
	"log"
	"sync"
	"time"
)

// PendingAudio representa un audio pendiente de ser entregado
type PendingAudio struct {
	SenderID   uint
	Channel    string
	AudioData  []byte
	Timestamp  time.Time
	Duration   float64
	SampleRate int
	Format     string
}

// AudioQueue maneja la cola de audios pendientes por usuario
type AudioQueue struct {
	mu     sync.RWMutex
	queues map[uint][]*PendingAudio // userID -> lista de audios pendientes
}

var globalAudioQueue = &AudioQueue{
	queues: make(map[uint][]*PendingAudio),
}

// EnqueueAudio agrega un audio a la cola de cada usuario del canal (excepto el sender)
func EnqueueAudio(senderID uint, channel string, audioData []byte, duration float64, recipients []uint) {
	globalAudioQueue.mu.Lock()
	defer globalAudioQueue.mu.Unlock()

	audio := &PendingAudio{
		SenderID:   senderID,
		Channel:    channel,
		AudioData:  audioData,
		Timestamp:  time.Now(),
		Duration:   duration,
		SampleRate: 16000,
		Format:     "wav",
	}

	// Agregar audio a la cola de cada recipient
	for _, recipientID := range recipients {
		if recipientID == senderID {
			continue // No agregar al sender
		}

		if globalAudioQueue.queues[recipientID] == nil {
			globalAudioQueue.queues[recipientID] = make([]*PendingAudio, 0, 10)
		}

		globalAudioQueue.queues[recipientID] = append(globalAudioQueue.queues[recipientID], audio)
		log.Printf("Audio encolado para usuario %d (de usuario %d, canal %s)", recipientID, senderID, channel)
	}

	// Limpiar audios antiguos (más de 5 minutos)
	go cleanOldAudios()
}

// DequeueAudio obtiene el siguiente audio pendiente para un usuario
func DequeueAudio(userID uint) *PendingAudio {
	globalAudioQueue.mu.Lock()
	defer globalAudioQueue.mu.Unlock()

	queue := globalAudioQueue.queues[userID]
	if len(queue) == 0 {
		return nil
	}

	// Obtener el primer audio (FIFO)
	audio := queue[0]
	globalAudioQueue.queues[userID] = queue[1:]

	log.Printf("Audio desencolado para usuario %d (de usuario %d, canal %s)", userID, audio.SenderID, audio.Channel)
	return audio
}

// GetPendingAudioCount obtiene la cantidad de audios pendientes para un usuario
func GetPendingAudioCount(userID uint) int {
	globalAudioQueue.mu.RLock()
	defer globalAudioQueue.mu.RUnlock()

	return len(globalAudioQueue.queues[userID])
}

// cleanOldAudios elimina audios más antiguos de 5 minutos
func cleanOldAudios() {
	globalAudioQueue.mu.Lock()
	defer globalAudioQueue.mu.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute)

	for userID, queue := range globalAudioQueue.queues {
		filtered := make([]*PendingAudio, 0, len(queue))
		for _, audio := range queue {
			if audio.Timestamp.After(cutoff) {
				filtered = append(filtered, audio)
			}
		}
		globalAudioQueue.queues[userID] = filtered

		// Eliminar cola vacía
		if len(filtered) == 0 {
			delete(globalAudioQueue.queues, userID)
		}
	}
}

// ConvertToAudioRelayResponse convierte un PendingAudio a AudioRelayResponse
func ConvertToAudioRelayResponse(audio *PendingAudio) AudioRelayResponse {
	return AudioRelayResponse{
		Status:      "received",
		Channel:     audio.Channel,
		Recipients:  []uint{},
		AudioBase64: base64.StdEncoding.EncodeToString(audio.AudioData),
		Duration:    audio.Duration,
		SampleRate:  audio.SampleRate,
		Format:      audio.Format,
	}
}
