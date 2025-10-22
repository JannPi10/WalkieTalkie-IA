package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	payload := map[string]string{"message": "hola"}

	WriteJSON(recorder, http.StatusCreated, payload)

	if recorder.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	var decoded map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if decoded["message"] != "hola" {
		t.Errorf("expected message 'hola', got %s", decoded["message"])
	}
}

func TestWriteErr(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteErr(recorder, http.StatusBadRequest, "fallo")

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}
	if ct := recorder.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	var decoded map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if decoded["error"] != "fallo" {
		t.Errorf("expected error 'fallo', got %s", decoded["error"])
	}
}
