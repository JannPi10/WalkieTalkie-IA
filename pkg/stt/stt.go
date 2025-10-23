package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
}

type transcriptionResponse struct {
	Text string `json:"text"`
}

func NewClient() (*Client, error) {
	baseURL := strings.TrimSpace(os.Getenv("STT_API_URL"))
	if baseURL == "" {
		return nil, fmt.Errorf("STT_API_URL no configurada")
	}
	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    baseURL,
	}, nil
}

func (c *Client) TranscribeAudio(ctx context.Context, audioData []byte) (string, error) {
	if len(audioData) == 0 {
		return "", fmt.Errorf("audio vacío")
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	filePart, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("crear form file: %w", err)
	}
	if _, err := filePart.Write(audioData); err != nil {
		return "", fmt.Errorf("escribir audio: %w", err)
	}

	if err := writer.WriteField("model", "Systran/faster-whisper-small"); err != nil {
		return "", fmt.Errorf("añadir modelo: %w", err)
	}
	if err := writer.WriteField("language", "es"); err != nil {
		return "", fmt.Errorf("añadir idioma: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, &buf)
	if err != nil {
		return "", fmt.Errorf("crear request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ejecutar request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("STT error %d: %s", resp.StatusCode, string(body))
	}

	var decoded transcriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decodificar respuesta: %w", err)
	}
	return strings.TrimSpace(decoded.Text), nil
}

func (c *Client) IsHumanSpeech(audioData []byte) bool {
	payload := audioData
	if len(payload) > 44 && string(payload[:4]) == "RIFF" && string(payload[8:12]) == "WAVE" {
		payload = payload[44:]
	}
	if len(payload) < 2000 {
		return false
	}

	samples := len(payload) / 2
	if samples == 0 {
		return false
	}

	var (
		sumSquares float64
		maxDelta   int
		prev       int16
	)

	for i := 0; i+1 < len(payload); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(payload[i : i+2]))
		sumSquares += float64(sample) * float64(sample)

		delta := int(sample - prev)
		if delta < 0 {
			delta = -delta
		}
		if delta > maxDelta {
			maxDelta = delta
		}
		prev = sample
	}

	rms := math.Sqrt(sumSquares / float64(samples))
	return rms > 300 || maxDelta > 250
}
