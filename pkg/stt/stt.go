// pkg/stt/stt.go
package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Client struct {
	scriptPath string
}

type transcriptionResponse struct {
	Text string `json:"text"`
}

func NewClient() (*Client, error) {
	scriptPath := strings.TrimSpace(os.Getenv("ASSEMBLYAI_SCRIPT_PATH"))
	if scriptPath == "" {
		scriptPath = "scripts/assemblyai_transcribe.py"
	}

	if !filepath.IsAbs(scriptPath) {
		if wd, err := os.Getwd(); err == nil {
			scriptPath = filepath.Join(wd, scriptPath)
		}
	}

	if _, err := os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("no se encontró el script STT en %s: %w", scriptPath, err)
	}

	return &Client{scriptPath: scriptPath}, nil
}

func (c *Client) TranscribeAudio(ctx context.Context, audioData []byte) (string, error) {
	if len(audioData) == 0 {
		return "", fmt.Errorf("audio vacío")
	}

	cmd := exec.CommandContext(ctx, "python3", c.scriptPath)
	cmd.Stdin = bytes.NewReader(audioData)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("assemblyai: %w - %s", err, strings.TrimSpace(stderr.String()))
	}

	var decoded transcriptionResponse
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
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
