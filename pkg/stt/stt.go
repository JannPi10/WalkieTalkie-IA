package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

type uploadResponse struct {
	UploadURL string `json:"upload_url"`
}

type transcriptRequest struct {
	AudioURL     string `json:"audio_url"`
	SpeechModel  string `json:"speech_model"`
	LanguageCode string `json:"language_code,omitempty"`
}

type transcriptResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Text   string `json:"text"`
	Error  string `json:"error"`
}

func NewClient() (*Client, error) {
	apiKey := strings.TrimSpace(os.Getenv("ASSEMBLYAI_API_KEY"))
	if apiKey == "" {
		return nil, fmt.Errorf("ASSEMBLYAI_API_KEY no está configurada")
	}

	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		baseURL:    "https://api.assemblyai.com/v2",
	}, nil
}

func (c *Client) TranscribeAudio(ctx context.Context, audioData []byte, format string) (string, error) {
	if len(audioData) == 0 {
		return "", fmt.Errorf("audio vacío")
	}

	uploadURL, err := c.uploadAudio(ctx, audioData, format)
	if err != nil {
		return "", fmt.Errorf("subir audio: %w", err)
	}

	transcriptID, err := c.createTranscript(ctx, uploadURL)
	if err != nil {
		return "", fmt.Errorf("crear transcripción: %w", err)
	}

	text, err := c.pollTranscript(ctx, transcriptID)
	if err != nil {
		return "", fmt.Errorf("obtener transcripción: %w", err)
	}

	return strings.TrimSpace(text), nil
}

func (c *Client) uploadAudio(ctx context.Context, audioData []byte, format string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/upload", bytes.NewReader(audioData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", format)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var upload uploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
		return "", err
	}

	return upload.UploadURL, nil
}

func (c *Client) createTranscript(ctx context.Context, audioURL string) (string, error) {
	reqBody := transcriptRequest{
		AudioURL:     audioURL,
		SpeechModel:  "universal",
		LanguageCode: "es",
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/transcript", bytes.NewReader(jsonData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var transcript transcriptResponse
	if err := json.NewDecoder(resp.Body).Decode(&transcript); err != nil {
		return "", err
	}

	return transcript.ID, nil
}

func (c *Client) pollTranscript(ctx context.Context, transcriptID string) (string, error) {
	url := fmt.Sprintf("%s/transcript/%s", c.baseURL, transcriptID)

	for {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return "", err
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", err
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}

		var transcript transcriptResponse
		if err := json.Unmarshal(body, &transcript); err != nil {
			return "", err
		}

		switch transcript.Status {
		case "completed":
			return transcript.Text, nil
		case "error":
			return "", fmt.Errorf("transcripción fallida: %s", transcript.Error)
		default:

			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
}

func (c *Client) IsHumanSpeech(audioData []byte) bool {
	if len(audioData) < 44 || string(audioData[:4]) != "RIFF" || string(audioData[8:12]) != "WAVE" {
		return false
	}

	payload := audioData[44:]
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
