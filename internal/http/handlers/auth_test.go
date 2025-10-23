package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupAuthTestDB(t *testing.T) func() {
	t.Helper()

	originalDB := config.DB

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite in-memory db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatalf("failed to migrate user model: %v", err)
	}

	config.DB = db

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		config.DB = originalDB
	}
}

func TestAuthenticate_MethodNotAllowed(t *testing.T) {
	cleanup := setupAuthTestDB(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/auth", nil)
	resp := httptest.NewRecorder()

	Authenticate(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, resp.Code)
	}

	if !strings.Contains(resp.Body.String(), `"método no permitido"`) {
		t.Errorf("unexpected body: %s", resp.Body.String())
	}
}

func TestAuthenticate_InvalidJSON(t *testing.T) {
	cleanup := setupAuthTestDB(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewBufferString("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	Authenticate(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}

	if !strings.Contains(resp.Body.String(), `"JSON inválido"`) {
		t.Errorf("unexpected body: %s", resp.Body.String())
	}
}

func TestAuthenticate_MissingFields(t *testing.T) {
	cleanup := setupAuthTestDB(t)
	defer cleanup()

	payload := map[string]any{"nombre": "   ", "pin": 0}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	Authenticate(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}

	if !strings.Contains(resp.Body.String(), `"nombre y pin son requeridos"`) {
		t.Errorf("unexpected body: %s", resp.Body.String())
	}
}

func TestAuthenticate_RegistersNewUser(t *testing.T) {
	cleanup := setupAuthTestDB(t)
	defer cleanup()

	payload := map[string]any{"nombre": "  Juan Pérez  ", "pin": 1234}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	Authenticate(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}

	var apiResp AuthenticationResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &apiResp); err != nil {
		t.Fatalf("unable to decode response: %v", err)
	}
	if apiResp.Token == "" {
		t.Fatal("expected non-empty token")
	}

	var user models.User
	if err := config.DB.Where("display_name = ?", "Juan Pérez").First(&user).Error; err != nil {
		t.Fatalf("user should exist: %v", err)
	}

	if user.AuthToken != apiResp.Token {
		t.Errorf("expected stored token to match API token")
	}
	if user.PinHash == "" {
		t.Error("expected pin hash to be stored")
	}
	if user.IsActive != true {
		t.Error("expected user to be marked active")
	}
	if user.LastActiveAt.IsZero() {
		t.Error("expected LastActiveAt to be set")
	}
}

func TestAuthenticate_ExistingUserWrongPin(t *testing.T) {
	cleanup := setupAuthTestDB(t)
	defer cleanup()

	hashed, err := bcrypt.GenerateFromPassword([]byte("1111"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash pin: %v", err)
	}

	user := models.User{
		DisplayName: "Juan",
		PinHash:     string(hashed),
	}
	if err := config.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	payload := map[string]any{"nombre": "Juan", "pin": 2222}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	Authenticate(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.Code)
	}

	var apiResp AuthenticationResponse
	_ = json.Unmarshal(resp.Body.Bytes(), &apiResp)
	if apiResp.Message != "credenciales inválidas" {
		t.Errorf("unexpected message: %s", apiResp.Message)
	}

	var stored models.User
	if err := config.DB.First(&stored, user.ID).Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}
	if stored.AuthToken != "" {
		t.Error("token should not be stored on failed authentication")
	}
}

func TestAuthenticate_ExistingUserWithoutPinHash(t *testing.T) {
	cleanup := setupAuthTestDB(t)
	defer cleanup()

	user := models.User{
		DisplayName: "María",
	}
	if err := config.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	payload := map[string]any{"nombre": "María", "pin": 9876}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/auth", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	Authenticate(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}

	var apiResp AuthenticationResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &apiResp); err != nil {
		t.Fatalf("unable to decode response: %v", err)
	}
	if apiResp.Token == "" {
		t.Fatal("expected non-empty token")
	}

	var stored models.User
	if err := config.DB.First(&stored, user.ID).Error; err != nil {
		t.Fatalf("failed to reload user: %v", err)
	}

	if stored.PinHash == "" {
		t.Error("pin hash should be set for legacy user")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.PinHash), []byte("9876")); err != nil {
		t.Error("stored pin hash does not match provided pin")
	}
	if stored.AuthToken != apiResp.Token {
		t.Error("expected stored token to match response token")
	}
}

func TestGenerateToken(t *testing.T) {
	token, err := generateToken(16)
	if err != nil {
		t.Fatalf("generateToken returned error: %v", err)
	}
	if len(token) != 32 {
		t.Fatalf("expected hex length 32, got %d", len(token))
	}
	if strings.Trim(token, "0123456789abcdef") != "" {
		t.Errorf("token should be lowercase hex, got %s", token)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		check    func(string) bool
	}{
		{"Juan Pérez", "juan.prez", nil},
		{"   María-Luisa   ", "maraluisa", nil},
		{"Foo   Bar!!", "foo...bar", nil},
		{"", "", func(s string) bool { return strings.HasPrefix(s, "user.") }},
	}

	for _, tc := range tests {
		got := slugify(tc.input)
		if tc.check != nil {
			if !tc.check(got) {
				t.Errorf("slugify(%q) = %q, does not satisfy custom check", tc.input, got)
			}
			continue
		}
		if got != tc.expected {
			t.Errorf("slugify(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}
