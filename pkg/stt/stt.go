package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	httpClient *http.Client
	baseURL    string
	authToken  string
}

type responsePayload struct {
	Text string `json:"text"`
}

func NewClient() (*Client, error) {
	token := strings.TrimSpace(os.Getenv("WIT_AI_TOKEN"))
	if token == "" {
		return nil, fmt.Errorf("WIT_AI_TOKEN no configurado")
	}

	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://api.wit.ai/speech?v=20241014",
		authToken:  token,
	}, nil
}

func (c *Client) TranscribeAudio(ctx context.Context, audioData []byte) (string, error) {
	if len(audioData) == 0 {
		return "", fmt.Errorf("audio vacío")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(audioData))
	if err != nil {
		return "", fmt.Errorf("error creando request: %w", err)
	}
	req.Header.Set("Authorization", c.authToken)
	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error ejecutando request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("Wit.ai error %d: %s", resp.StatusCode, string(body))
	}

	var decoded responsePayload
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("error decodificando respuesta: %w", err)
	}

	return strings.TrimSpace(decoded.Text), nil
}

func (c *Client) IsHumanSpeech(audioData []byte) bool {
	if len(audioData) < 1500 {
		return false
	}

	var variations int
	threshold := byte(40)

	for i := 1; i < len(audioData); i++ {
		if abs(int(audioData[i])-int(audioData[i-1])) > int(threshold) {
			variations++
		}
	}
	return variations > len(audioData)/25
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
