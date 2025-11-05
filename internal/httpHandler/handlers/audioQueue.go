package handlers

import (
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
	queues map[uint][]*PendingAudio
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

	for _, recipientID := range recipients {
		if recipientID == senderID {
			continue
		}

		if globalAudioQueue.queues[recipientID] == nil {
			globalAudioQueue.queues[recipientID] = make([]*PendingAudio, 0, 10)
		}

		globalAudioQueue.queues[recipientID] = append(globalAudioQueue.queues[recipientID], audio)
		log.Printf("Audio encolado para usuario %d (de usuario %d, canal %s)", recipientID, senderID, channel)
	}

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

	audio := queue[0]
	globalAudioQueue.queues[userID] = queue[1:]

	log.Printf("Audio desencolado para usuario %d (de usuario %d, canal %s)", userID, audio.SenderID, audio.Channel)
	return audio
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

		if len(filtered) == 0 {
			delete(globalAudioQueue.queues, userID)
		}
	}
}

// ClearPendingAudio elimina la cola completa de un usuario
func ClearPendingAudio(userID uint) {
	globalAudioQueue.mu.Lock()
	defer globalAudioQueue.mu.Unlock()
	delete(globalAudioQueue.queues, userID)
}
