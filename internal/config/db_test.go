package config

import (
	"testing"

	"walkie-backend/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	if err := db.AutoMigrate(&models.Channel{}, &models.User{}); err != nil {
		t.Fatalf("failed to migrate models: %v", err)
	}

	return db
}

func TestSeedDatabase_CreatesInitialData(t *testing.T) {
	db := setupTestDB(t)

	seedDatabase(db)

	var channelCount int64
	if err := db.Model(&models.Channel{}).Count(&channelCount).Error; err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if channelCount != 5 {
		t.Fatalf("expected 5 channels, got %d", channelCount)
	}

	var userCount int64
	if err := db.Model(&models.User{}).Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 10 {
		t.Fatalf("expected 10 users, got %d", userCount)
	}
}

func TestSeedDatabase_IsIdempotent(t *testing.T) {
	db := setupTestDB(t)

	seedDatabase(db)
	seedDatabase(db)

	var channelCount int64
	if err := db.Model(&models.Channel{}).Count(&channelCount).Error; err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if channelCount != 5 {
		t.Fatalf("expected 5 channels after reseed, got %d", channelCount)
	}

	var userCount int64
	if err := db.Model(&models.User{}).Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 10 {
		t.Fatalf("expected 10 users after reseed, got %d", userCount)
	}
}
