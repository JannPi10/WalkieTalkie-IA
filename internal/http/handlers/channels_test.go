package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupChannelsTestDB(t *testing.T) func() {
	t.Helper()

	originalDB := config.DB

	dsn := fmt.Sprintf(
		"file:%s?mode=memory&cache=shared",
		strings.ReplaceAll(t.Name(), "/", "_"),
	)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite in-memory db: %v", err)
	}

	if err := db.AutoMigrate(&models.User{}, &models.Channel{}, &models.ChannelMembership{}); err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}

	config.DB = db

	return func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		config.DB = originalDB
	}
}

func TestListPublicChannels_Success(t *testing.T) {
	cleanup := setupChannelsTestDB(t)
	defer cleanup()

	publicChannels := []models.Channel{
		{Code: "canal-1", Name: "Canal 1", MaxUsers: 100, IsPrivate: false},
		{Code: "canal-2", Name: "Canal 2", MaxUsers: 50, IsPrivate: false},
		{Code: "private-1", Name: "Private 1", MaxUsers: 10, IsPrivate: true},
	}
	for _, ch := range publicChannels {
		if err := config.DB.Create(&ch).Error; err != nil {
			t.Fatalf("failed to seed channel: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/channels/public", nil)
	resp := httptest.NewRecorder()

	ListPublicChannels(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}

	var channels []map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &channels); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(channels) != 2 {
		t.Fatalf("expected 2 public channels, got %d", len(channels))
	}

	expected := map[string]bool{"canal-1": false, "canal-2": false}
	for _, ch := range channels {
		code, ok := ch["code"].(string)
		if !ok {
			t.Errorf("expected code to be string, got %T", ch["code"])
			continue
		}
		if _, exists := expected[code]; exists {
			expected[code] = true
		} else {
			t.Errorf("unexpected channel code: %s", code)
		}
	}
	for code, found := range expected {
		if !found {
			t.Errorf("expected to find channel %s", code)
		}
	}
}

func TestChannelUsers_MissingChannel(t *testing.T) {
	cleanup := setupChannelsTestDB(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/channel-users", nil)
	resp := httptest.NewRecorder()

	ChannelUsers(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}

	var errResp map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}

	if errResp["error"] != "Canal inválido" {
		t.Errorf("expected error 'Canal inválido', got %s", errResp["error"])
	}
}

func TestChannelUsers_ChannelNotFound(t *testing.T) {
	cleanup := setupChannelsTestDB(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/channel-users?channel=nonexistent", nil)
	resp := httptest.NewRecorder()

	ChannelUsers(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, resp.Code)
	}

	var errResp map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}

	if errResp["error"] != "Canal no encontrado" {
		t.Errorf("expected error 'Canal no encontrado', got %s", errResp["error"])
	}
}

func TestChannelUsers_Success(t *testing.T) {
	cleanup := setupChannelsTestDB(t)
	defer cleanup()

	channel := models.Channel{
		Code:      "channel-users-1",
		Name:      "Canal 1",
		MaxUsers:  100,
		IsPrivate: false,
	}
	if err := config.DB.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	users := []models.User{
		{DisplayName: "Juan"},
		{DisplayName: "María"},
	}
	for i := range users {
		if err := config.DB.Create(&users[i]).Error; err != nil {
			t.Fatalf("failed to seed user: %v", err)
		}
	}

	memberships := []models.ChannelMembership{
		{UserID: users[0].ID, ChannelID: channel.ID, Active: true},
		{UserID: users[1].ID, ChannelID: channel.ID, Active: true},
	}
	for _, m := range memberships {
		if err := config.DB.Create(&m).Error; err != nil {
			t.Fatalf("failed to seed membership: %v", err)
		}
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/channel-users?channel=channel-users-1",
		nil,
	)
	resp := httptest.NewRecorder()

	ChannelUsers(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.Code)
	}

	var members []map[string]interface{}
	if err := json.Unmarshal(resp.Body.Bytes(), &members); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(members) != 2 {
		t.Fatalf("expected 2 active members, got %d", len(members))
	}

	expectedNames := map[string]bool{"Juan": false, "María": false}
	for _, m := range members {
		name, ok := m["displayName"].(string)
		if !ok {
			t.Errorf("expected displayName to be string, got %T", m["displayName"])
			continue
		}
		if _, exists := expectedNames[name]; exists {
			expectedNames[name] = true
		} else {
			t.Errorf("unexpected user name: %s", name)
		}
	}
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected to find user %s", name)
		}
	}
}
