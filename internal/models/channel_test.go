package models

import (
	"fmt"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupChannelTestDB(t *testing.T) *gorm.DB {
	t.Helper()

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

	if err := db.AutoMigrate(&Channel{}, &User{}, &ChannelMembership{}); err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})

	return db
}

func TestChannel_BasicStruct(t *testing.T) {
	db := setupChannelTestDB(t)

	channel := Channel{
		Code: "test-channel-1",
		Name: "Mi Canal",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	if channel.Code != "test-channel-1" {
		t.Errorf("Expected Code 'test-channel-1', got '%s'", channel.Code)
	}

	if channel.Name != "Mi Canal" {
		t.Errorf("Expected Name 'Mi Canal', got '%s'", channel.Name)
	}

	users := []User{
		{DisplayName: "Juan"},
		{DisplayName: "María"},
		{DisplayName: "Pedro"},
	}
	for i := range users {
		if err := db.Create(&users[i]).Error; err != nil {
			t.Fatalf("failed to seed user: %v", err)
		}
	}

	memberships := []ChannelMembership{
		{UserID: users[0].ID, ChannelID: channel.ID, Active: true},
		{UserID: users[1].ID, ChannelID: channel.ID, Active: true},
		{UserID: users[2].ID, ChannelID: channel.ID, Active: true}, // se desactiva luego
	}
	for i := range memberships {
		if err := db.Create(&memberships[i]).Error; err != nil {
			t.Fatalf("failed to seed membership: %v", err)
		}
	}

	// Marcar la tercera membresía como inactiva
	memberships[2].Deactivate()
	if err := db.Model(&memberships[2]).Updates(map[string]interface{}{
		"active":  memberships[2].Active,
		"left_at": memberships[2].LeftAt,
	}).Error; err != nil {
		t.Fatalf("failed to deactivate membership: %v", err)
	}

	activeMembers, err := channel.GetActiveMembers(db)
	if err != nil {
		t.Fatalf("GetActiveMembers returned error: %v", err)
	}
	if len(activeMembers) != 2 {
		t.Fatalf("expected 2 active members, got %d", len(activeMembers))
	}

	expectedIDs := map[uint]bool{
		users[0].ID: false,
		users[1].ID: false,
	}
	for _, m := range activeMembers {
		if _, ok := expectedIDs[m.UserID]; !ok {
			t.Errorf("unexpected active member userID: %d", m.UserID)
		} else {
			expectedIDs[m.UserID] = true
		}
	}
	for id, found := range expectedIDs {
		if !found {
			t.Errorf("expected to find active member with userID %d", id)
		}
	}

	activeCount, err := channel.GetActiveMemberCount(db)
	if err != nil {
		t.Fatalf("GetActiveMemberCount returned error: %v", err)
	}
	if activeCount != 2 {
		t.Fatalf("expected active member count 2, got %d", activeCount)
	}
}
