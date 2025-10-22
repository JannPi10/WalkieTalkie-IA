package handlers

import (
	"sync"

	"walkie-backend/pkg/deepseek"
	"walkie-backend/pkg/stt"
)

var (
	onceDeepseek sync.Once
	dsClient     *deepseek.Client
	dsErr        error

	onceSTT sync.Once
	sClient *stt.Client
	sErr    error
)

func EnsureDeepseekClient() (*deepseek.Client, error) {
	onceDeepseek.Do(func() {
		dsClient, dsErr = deepseek.NewClient()
	})
	return dsClient, dsErr
}

func EnsureSTTClient() (*stt.Client, error) {
	onceSTT.Do(func() {
		sClient, sErr = stt.NewClient()
	})
	return sClient, sErr
}
