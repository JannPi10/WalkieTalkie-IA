package stt

//
//import (
//	"context"
//	"net/http"
//	"net/http/httptest"
//	"testing"
//)
//
//func TestNewClient(t *testing.T) {
//	t.Setenv("STT_API_URL", "http://example.com")
//	client, err := NewClient()
//	if err != nil {
//		t.Fatalf("NewClient() error = %v", err)
//	}
//	if client.baseURL != "http://example.com" {
//		t.Errorf("expected baseURL 'http://example.com', got %s", client.baseURL)
//	}
//}
//
//func TestTranscribeAudio_Success(t *testing.T) {
//	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		if r.Method != "POST" {
//			t.Errorf("expected POST, got %s", r.Method)
//		}
//		response := `{"text": "Hola, esto es una prueba de transcripción"}`
//		w.Header().Set("Content-Type", "application/json")
//		w.Write([]byte(response))
//	}))
//	defer server.Close()
//
//	client := &Client{
//		baseURL:    server.URL,
//		httpClient: &http.Client{},
//	}
//
//	audioData := []byte("mock wav data")
//	result, err := client.TranscribeAudio(context.Background(), audioData)
//	if err != nil {
//		t.Fatalf("TranscribeAudio() error = %v", err)
//	}
//
//	expected := "Hola, esto es una prueba de transcripción"
//	if result != expected {
//		t.Errorf("expected %q, got %q", expected, result)
//	}
//}
//
//func TestTranscribeAudio_HTTPError(t *testing.T) {
//	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		w.WriteHeader(http.StatusInternalServerError)
//		w.Write([]byte("Internal Server Error"))
//	}))
//	defer server.Close()
//
//	client := &Client{
//		baseURL:    server.URL,
//		httpClient: &http.Client{},
//	}
//
//	_, err := client.TranscribeAudio(context.Background(), []byte("mock wav data"))
//	if err == nil {
//		t.Error("expected error, got none")
//	}
//}
//
//func TestTranscribeAudio_InvalidJSON(t *testing.T) {
//	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		w.Header().Set("Content-Type", "application/json")
//		w.Write([]byte("invalid json"))
//	}))
//	defer server.Close()
//
//	client := &Client{
//		baseURL:    server.URL,
//		httpClient: &http.Client{},
//	}
//
//	_, err := client.TranscribeAudio(context.Background(), []byte("mock wav data"))
//	if err == nil {
//		t.Error("expected error, got none")
//	}
//}
//
//func TestTranscribeAudio_EmptyAudio(t *testing.T) {
//	client := &Client{
//		baseURL:    "http://example.com",
//		httpClient: &http.Client{},
//	}
//
//	_, err := client.TranscribeAudio(context.Background(), []byte{})
//	if err == nil {
//		t.Error("expected error for empty audio")
//	}
//}
//
//func TestTranscribeAudio_NetworkError(t *testing.T) {
//	client := &Client{
//		baseURL:    "http://invalid-url",
//		httpClient: &http.Client{},
//	}
//
//	_, err := client.TranscribeAudio(context.Background(), []byte("mock wav data"))
//	if err == nil {
//		t.Error("expected network error")
//	}
//}
//
//func TestIsHumanSpeech(t *testing.T) {
//	tests := []struct {
//		name      string
//		audioData []byte
//		expected  bool
//	}{
//		{
//			name:      "empty audio",
//			audioData: []byte{},
//			expected:  false,
//		},
//		{
//			name:      "short audio",
//			audioData: make([]byte, 1000),
//			expected:  false,
//		},
//		{
//			name:      "low amplitude audio",
//			audioData: createLowAmplitudeAudio(3000),
//			expected:  false,
//		},
//		{
//			name:      "high amplitude audio",
//			audioData: createHighAmplitudeAudio(3000),
//			expected:  true,
//		},
//		{
//			name:      "WAV header with low amplitude",
//			audioData: append(wavHeader(), createLowAmplitudeAudio(3000)...),
//			expected:  false,
//		},
//		{
//			name:      "WAV header with high amplitude",
//			audioData: append(wavHeader(), createHighAmplitudeAudio(3000)...),
//			expected:  true,
//		},
//	}
//
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			client := &Client{}
//			result := client.IsHumanSpeech(tt.audioData)
//			if result != tt.expected {
//				t.Errorf("IsHumanSpeech() = %v, expected %v", result, tt.expected)
//			}
//		})
//	}
//}
//
//func wavHeader() []byte {
//	header := make([]byte, 44)
//	copy(header[0:4], "RIFF")
//	copy(header[8:12], "WAVE")
//	copy(header[12:16], "fmt ")
//	copy(header[16:20], []byte{16, 0, 0, 0})
//	copy(header[20:22], []byte{1, 0})
//	copy(header[22:24], []byte{128, 62})
//	copy(header[24:28], []byte{128, 62, 0, 0})
//	copy(header[28:30], []byte{2, 0})
//	copy(header[30:32], []byte{16, 0})
//	copy(header[32:36], "data")
//	copy(header[36:40], []byte{0, 0, 0, 0})
//	return header
//}
//
//func createLowAmplitudeAudio(size int) []byte {
//	data := make([]byte, size)
//	for i := 0; i < len(data); i += 2 {
//		sample := int16(50)
//		data[i] = byte(sample & 0xff)
//		data[i+1] = byte((sample >> 8) & 0xff)
//	}
//	return data
//}
//
//func createHighAmplitudeAudio(size int) []byte {
//	data := make([]byte, size)
//	for i := 0; i < len(data); i += 2 {
//		sample := int16(1000 + (i % 500))
//		data[i] = byte(sample & 0xff)
//		data[i+1] = byte((sample >> 8) & 0xff)
//	}
//	return data
//}
