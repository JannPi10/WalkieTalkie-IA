package config

import (
	"reflect"
	"sync"
	"testing"

	"walkie-backend/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func resetOnce(once *sync.Once) {
	onceV := reflect.ValueOf(once).Elem()
	onceV.Set(reflect.ValueOf(sync.Once{}))
}

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

func TestConnectAndMigrate(t *testing.T) {
	db, err := connectAndMigrate(":memory:")
	if err != nil {
		t.Fatalf("connectAndMigrate failed: %v", err)
	}

	if !db.Migrator().HasTable(&models.User{}) {
		t.Error("User table not created")
	}
	if !db.Migrator().HasTable(&models.Channel{}) {
		t.Error("Channel table not created")
	}
	if !db.Migrator().HasTable(&models.ChannelMembership{}) {
		t.Error("ChannelMembership table not created")
	}

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

func TestConnectDB(t *testing.T) {
	resetOnce(&once)
	oldDB := DB
	defer func() { DB = oldDB }()

	t.Setenv("DATABASE_URL", ":memory:")
	ConnectDB()

	if DB == nil {
		t.Fatal("DB should be assigned")
	}

	if !DB.Migrator().HasTable(&models.User{}) {
		t.Error("User table not created")
	}
	if !DB.Migrator().HasTable(&models.Channel{}) {
		t.Error("Channel table not created")
	}
	if !DB.Migrator().HasTable(&models.ChannelMembership{}) {
		t.Error("ChannelMembership table not created")
	}

	var channelCount int64
	if err := DB.Model(&models.Channel{}).Count(&channelCount).Error; err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if channelCount != 5 {
		t.Fatalf("expected 5 channels, got %d", channelCount)
	}

	var userCount int64
	if err := DB.Model(&models.User{}).Count(&userCount).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 10 {
		t.Fatalf("expected 10 users, got %d", userCount)
	}
}
