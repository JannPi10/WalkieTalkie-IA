package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewClient(t *testing.T) {
	t.Run("API key is set", func(t *testing.T) {
		t.Setenv("ASSEMBLYAI_API_KEY", "test-api-key")
		client, err := NewClient()
		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-api-key", client.apiKey)
	})

	t.Run("API key is not set", func(t *testing.T) {
		t.Setenv("ASSEMBLYAI_API_KEY", "")
		_, err := NewClient()
		assert.Error(t, err)
		assert.Equal(t, "ASSEMBLYAI_API_KEY no está configurada", err.Error())
	})
}

// mockAssemblyAIServer creates a test server that simulates the AssemblyAI API flow.
func mockAssemblyAIServer(t *testing.T, pollCountUntilComplete int, finalStatus string, finalError string) *httptest.Server {
	var pollCounter int32
	const transcriptID = "test-transcript-id"

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "Authorization header is required", http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/upload":
			assert.Equal(t, http.MethodPost, r.Method)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(uploadResponse{UploadURL: "https://cdn.assemblyai.com/upload/mock-upload-url"})
		case "/transcript":
			assert.Equal(t, http.MethodPost, r.Method)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(transcriptResponse{ID: transcriptID, Status: "queued"})
		case fmt.Sprintf("/transcript/%s", transcriptID):
			assert.Equal(t, http.MethodGet, r.Method)

			currentPoll := atomic.AddInt32(&pollCounter, 1)
			status := "processing"
			if int(currentPoll) >= pollCountUntilComplete {
				status = finalStatus
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(transcriptResponse{
				ID:     transcriptID,
				Status: status,
				Text:   "This is a test transcript.",
				Error:  finalError,
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTranscribeAudio_Success(t *testing.T) {
	// Simulate a server that takes 2 polls to complete
	server := mockAssemblyAIServer(t, 2, "completed", "")
	defer server.Close()

	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	client, err := NewClient()
	assert.NoError(t, err)

	// Point the client to the mock server
	client.baseURL = server.URL
	client.httpClient = server.Client()

	ctx := context.Background()
	text, err := client.TranscribeAudio(ctx, []byte("test audio data"), "audio/wav")

	assert.NoError(t, err)
	assert.Equal(t, "This is a test transcript.", text)
}

func TestTranscribeAudio_Failure(t *testing.T) {
	// Simulate a server that fails immediately
	server := mockAssemblyAIServer(t, 1, "error", "something went wrong")
	defer server.Close()

	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	client, err := NewClient()
	assert.NoError(t, err)

	client.baseURL = server.URL
	client.httpClient = server.Client()

	_, err = client.TranscribeAudio(context.Background(), []byte("test audio data"), "audio/wav")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "transcripción fallida: something went wrong")
}

func TestTranscribeAudio_EmptyAudio(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	client, err := NewClient()
	assert.NoError(t, err)

	_, err = client.TranscribeAudio(context.Background(), []byte{}, "audio/wav")
	assert.Error(t, err)
	assert.Equal(t, "audio vacío", err.Error())
}

func TestIsHumanSpeech(t *testing.T) {
	tests := []struct {
		name      string
		audioData []byte
		expected  bool
	}{
		{"empty audio", []byte{}, false},
		{"short audio", make([]byte, 1999), false},
		{"low amplitude", createSineWave(4000, 100), false},
		{"high amplitude", createSineWave(4000, 500), true},
		{"high delta", createSquareWave(4000, 1000), true},
		{"wav header and high amplitude", append(wavHeader(), createSineWave(4000, 600)...), true},
		{"no samples", make([]byte, 1), false},
	}

	client := &Client{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, client.IsHumanSpeech(tt.audioData))
		})
	}
}

func TestTranscribeAudio_ContextCancellation(t *testing.T) {
	server := mockAssemblyAIServer(t, 5, "completed", "") // Will never complete in time
	defer server.Close()

	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	client, err := NewClient()
	assert.NoError(t, err)

	client.baseURL = server.URL
	client.httpClient = server.Client()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = client.TranscribeAudio(ctx, []byte("test audio data"), "audio/wav")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), context.DeadlineExceeded.Error())
}

func TestUploadAudio_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upload failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	client, err := NewClient()
	assert.NoError(t, err)
	client.baseURL = server.URL
	client.httpClient = server.Client()

	_, err = client.uploadAudio(context.Background(), []byte("audio"), "audio/wav")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500: upload failed")
}

func TestCreateTranscript_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "transcript failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	client, err := NewClient()
	assert.NoError(t, err)
	client.baseURL = server.URL
	client.httpClient = server.Client()

	_, err = client.createTranscript(context.Background(), "audio-url")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500: transcript failed")
}

// Helper functions for generating test audio data

func wavHeader() []byte {
	// Creates a minimal 44-byte WAV header.
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	copy(header[8:12], "WAVE")
	return header
}

func createSineWave(size int, amplitude int16) []byte {
	data := make([]byte, size)
	for i := 0; i < len(data); i += 2 {
		// Simple sine wave generation is complex, so we'll just use a fixed value for simplicity
		// to control the RMS and delta.
		sample := amplitude
		if (i/2)%2 == 0 {
			sample = -amplitude
		}
		data[i] = byte(sample & 0xff)
		data[i+1] = byte((sample >> 8) & 0xff)
	}
	return append(wavHeader(), data...)
}

func createSquareWave(size int, amplitude int16) []byte {
	data := make([]byte, size)
	for i := 0; i < len(data); i += 2 {
		sample := amplitude
		if (i/500)%2 == 0 {
			sample = -amplitude
		}
		data[i] = byte(sample & 0xff)
		data[i+1] = byte((sample >> 8) & 0xff)
	}
	return append(wavHeader(), data...)
}

func TestTranscribeAudio_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("ASSEMBLYAI_API_KEY", "test-key")
	client, err := NewClient()
	assert.NoError(t, err)

	client.baseURL = server.URL
	client.httpClient.Timeout = 10 * time.Millisecond

	_, err = client.TranscribeAudio(context.Background(), []byte("some audio data"), "audio/wav")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}