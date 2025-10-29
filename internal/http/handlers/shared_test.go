package handlers

import (
	"reflect"
	"sync"
	"testing"
)

func resetOnce(once *sync.Once, client interface{}, err interface{}) {
	onceV := reflect.ValueOf(once).Elem()
	onceV.Set(reflect.ValueOf(sync.Once{}))

	clientV := reflect.ValueOf(client).Elem()
	clientV.Set(reflect.Zero(clientV.Type()))

	errV := reflect.ValueOf(err).Elem()
	errV.Set(reflect.Zero(errV.Type()))
}

func TestEnsureDeepseekClient(t *testing.T) {
	resetOnce(&onceAI, &aiClient, &aiErr)

	client1, err1 := EnsureAIClient()

	client2, err2 := EnsureAIClient()

	if client1 != client2 {
		t.Errorf("Expected same client, got different")
	}
	if err1 != err2 {
		t.Errorf("Expected same error, got different: %v vs %v", err1, err2)
	}

	if err1 == nil && client1 == nil {
		t.Errorf("Client should not be nil on success")
	}
}

func TestEnsureSTTClient(t *testing.T) {
	resetOnce(&onceSTT, &sClient, &sErr)

	client1, err1 := EnsureSTTClient()

	client2, err2 := EnsureSTTClient()

	if client1 != client2 {
		t.Errorf("Expected same client, got different")
	}
	if err1 != err2 {
		t.Errorf("Expected same error, got different: %v vs %v", err1, err2)
	}

	if err1 == nil && client1 == nil {
		t.Errorf("Client should not be nil on success")
	}
}
