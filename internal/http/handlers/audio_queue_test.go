package handlers

import (
	"testing"
	"time"
)

func TestEnqueueAudio(t *testing.T) {
	// Limpiar la cola global antes de cada test
	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues = make(map[uint][]*PendingAudio)
	globalAudioQueue.mu.Unlock()

	senderID := uint(1)
	channel := "test-channel"
	audioData := []byte("test audio data")
	duration := 2.5
	recipients := []uint{2, 3, 4}

	EnqueueAudio(senderID, channel, audioData, duration, recipients)

	// Verificar que los recipients tienen el audio encolado
	for _, recipientID := range recipients {
		globalAudioQueue.mu.RLock()
		queueLen := len(globalAudioQueue.queues[recipientID])
		globalAudioQueue.mu.RUnlock()
		if queueLen != 1 {
			t.Errorf("Expected 1 audio for recipient %d, got %d", recipientID, queueLen)
		}

		audio := DequeueAudio(recipientID)
		if audio == nil {
			t.Errorf("Expected audio for recipient %d", recipientID)
		} else {
			if audio.SenderID != senderID {
				t.Errorf("Expected senderID %d, got %d", senderID, audio.SenderID)
			}
			if audio.Channel != channel {
				t.Errorf("Expected channel %s, got %s", channel, audio.Channel)
			}
			if string(audio.AudioData) != string(audioData) {
				t.Errorf("Audio data mismatch")
			}
			if audio.Duration != duration {
				t.Errorf("Expected duration %f, got %f", duration, audio.Duration)
			}
		}
	}
}

func TestEnqueueAudio_ExcludesSender(t *testing.T) {
	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues = make(map[uint][]*PendingAudio)
	globalAudioQueue.mu.Unlock()

	senderID := uint(1)
	recipients := []uint{1, 2} // Incluye al sender

	EnqueueAudio(senderID, "test", []byte("data"), 1.0, recipients)

	// El sender no debería tener audio
	globalAudioQueue.mu.RLock()
	senderQueueLen := len(globalAudioQueue.queues[senderID])
	globalAudioQueue.mu.RUnlock()
	if senderQueueLen != 0 {
		t.Errorf("Sender should not have audio, got %d", senderQueueLen)
	}

	// Recipient 2 sí
	globalAudioQueue.mu.RLock()
	recipientQueueLen := len(globalAudioQueue.queues[2])
	globalAudioQueue.mu.RUnlock()
	if recipientQueueLen != 1 {
		t.Errorf("Recipient 2 should have 1 audio, got %d", recipientQueueLen)
	}
}

func TestDequeueAudio(t *testing.T) {
	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues = make(map[uint][]*PendingAudio)
	globalAudioQueue.mu.Unlock()

	userID := uint(5)
	audio1 := &PendingAudio{SenderID: 1, Channel: "ch1", AudioData: []byte("audio1"), Timestamp: time.Now(), Duration: 1.0}
	audio2 := &PendingAudio{SenderID: 2, Channel: "ch2", AudioData: []byte("audio2"), Timestamp: time.Now(), Duration: 2.0}

	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues[userID] = []*PendingAudio{audio1, audio2}
	globalAudioQueue.mu.Unlock()

	// Dequeue first
	dequeued := DequeueAudio(userID)
	if dequeued == nil || string(dequeued.AudioData) != "audio1" {
		t.Errorf("Expected audio1, got %v", dequeued)
	}

	// Dequeue second
	dequeued2 := DequeueAudio(userID)
	if dequeued2 == nil || string(dequeued2.AudioData) != "audio2" {
		t.Errorf("Expected audio2, got %v", dequeued2)
	}

	// Queue should be empty
	dequeued3 := DequeueAudio(userID)
	if dequeued3 != nil {
		t.Errorf("Expected nil, got %v", dequeued3)
	}
}

func TestDequeueAudio_EmptyQueue(t *testing.T) {
	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues = make(map[uint][]*PendingAudio)
	globalAudioQueue.mu.Unlock()

	userID := uint(10)
	audio := DequeueAudio(userID)
	if audio != nil {
		t.Errorf("Expected nil for empty queue, got %v", audio)
	}
}

func TestCleanOldAudios(t *testing.T) {
	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues = make(map[uint][]*PendingAudio)
	globalAudioQueue.mu.Unlock()

	userID := uint(9)
	now := time.Now()
	oldAudio := &PendingAudio{Timestamp: now.Add(-10 * time.Minute), AudioData: []byte("old")}
	newAudio := &PendingAudio{Timestamp: now.Add(-1 * time.Minute), AudioData: []byte("new")}

	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues[userID] = []*PendingAudio{oldAudio, newAudio}
	globalAudioQueue.mu.Unlock()

	cleanOldAudios()

	globalAudioQueue.mu.RLock()
	queueLen := len(globalAudioQueue.queues[userID])
	globalAudioQueue.mu.RUnlock()
	if queueLen != 1 {
		t.Errorf("Expected 1 audio after cleanup, got %d", queueLen)
	}

	dequeued := DequeueAudio(userID)
	if dequeued == nil || string(dequeued.AudioData) != "new" {
		t.Errorf("Expected new audio, got %v", dequeued)
	}
}

func TestCleanOldAudios_AllOld(t *testing.T) {
	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues = make(map[uint][]*PendingAudio)
	globalAudioQueue.mu.Unlock()

	userID := uint(10)
	oldAudio := &PendingAudio{Timestamp: time.Now().Add(-10 * time.Minute)}

	globalAudioQueue.mu.Lock()
	globalAudioQueue.queues[userID] = []*PendingAudio{oldAudio}
	globalAudioQueue.mu.Unlock()

	cleanOldAudios()

	globalAudioQueue.mu.RLock()
	queueLen := len(globalAudioQueue.queues[userID])
	_, exists := globalAudioQueue.queues[userID]
	globalAudioQueue.mu.RUnlock()
	if queueLen != 0 {
		t.Errorf("Expected 0 audios after cleanup, got %d", queueLen)
	}
	if exists {
		t.Errorf("Queue should be deleted")
	}
}
