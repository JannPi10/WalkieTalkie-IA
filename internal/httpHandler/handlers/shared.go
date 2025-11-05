package handlers

import (
	"sync"

	"walkie-backend/pkg/qwen"
	"walkie-backend/pkg/stt"
)

var (
	onceAI   sync.Once
	aiClient *qwen.Client
	aiErr    error

	onceSTT sync.Once
	sClient *stt.Client
	sErr    error
)

func EnsureAIClient() (*qwen.Client, error) {
	onceAI.Do(func() {
		aiClient, aiErr = qwen.NewClient()
	})
	return aiClient, aiErr
}

func EnsureSTTClient() (*stt.Client, error) {
	onceSTT.Do(func() {
		sClient, sErr = stt.NewClient()
	})
	return sClient, sErr
}
