package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupUserServiceTestDB(t *testing.T) func() {
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

func TestUserServiceConnectUserToChannel_NewMembership(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	user := models.User{DisplayName: "Juan"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	channel := models.Channel{
		Code:     "canal-1",
		Name:     "Canal 1",
		MaxUsers: 2,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	service := NewUserService()
	if err := service.ConnectUserToChannel(user.ID, "canal-1"); err != nil {
		t.Fatalf("ConnectUserToChannel returned error: %v", err)
	}

	var membership models.ChannelMembership
	if err := db.Where("user_id = ? AND channel_id = ?", user.ID, channel.ID).First(&membership).Error; err != nil {
		t.Fatalf("membership not created: %v", err)
	}
	if !membership.Active {
		t.Errorf("expected membership active, got inactive")
	}

	var updatedUser models.User
	if err := db.First(&updatedUser, user.ID).Error; err != nil {
		t.Fatalf("failed to fetch updated user: %v", err)
	}
	if updatedUser.CurrentChannelID == nil || *updatedUser.CurrentChannelID != channel.ID {
		t.Errorf("expected user current channel to be %d, got %v", channel.ID, updatedUser.CurrentChannelID)
	}
}

func TestUserServiceConnectUserToChannel_ChannelFull(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	channel := models.Channel{
		Code:     "canal-full",
		Name:     "Canal Full",
		MaxUsers: 1,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	user1 := models.User{DisplayName: "User1"}
	user2 := models.User{DisplayName: "User2"}
	if err := db.Create(&user1).Error; err != nil {
		t.Fatalf("failed to seed user1: %v", err)
	}
	if err := db.Create(&user2).Error; err != nil {
		t.Fatalf("failed to seed user2: %v", err)
	}

	service := NewUserService()
	if err := service.ConnectUserToChannel(user1.ID, "canal-full"); err != nil {
		t.Fatalf("unexpected error connecting first user: %v", err)
	}

	if err := service.ConnectUserToChannel(user2.ID, "canal-full"); err == nil {
		t.Fatalf("expected error when channel is full, got nil")
	}
}

func TestUserServiceConnectUserToChannel_ChannelNotFound(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	service := NewUserService()
	if err := service.ConnectUserToChannel(999, "missing"); err == nil || !strings.Contains(err.Error(), "canal no encontrado") {
		t.Fatalf("expected channel not found error, got %v", err)
	}
}

func TestUserServiceConnectUserToChannel_ActiveCountError(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	user := models.User{DisplayName: "Luis"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	channel := models.Channel{
		Code:     "cap-error",
		Name:     "Cap Error",
		MaxUsers: 5,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	if err := db.Migrator().DropTable(&models.ChannelMembership{}); err != nil {
		t.Fatalf("failed to drop table: %v", err)
	}

	service := NewUserService()
	err := service.ConnectUserToChannel(user.ID, "cap-error")
	if err == nil || !strings.Contains(err.Error(), "error verificando capacidad del canal") {
		t.Fatalf("expected capacity error, got %v", err)
	}
}

func TestUserServiceConnectUserToChannel_DisconnectError(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	channel := models.Channel{
		Code: "disc-error",
		Name: "Disc Error",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	service := NewUserService()
	err := service.ConnectUserToChannel(12345, "disc-error")
	if err == nil || !strings.Contains(err.Error(), "error desconectando del canal actual") {
		t.Fatalf("expected disconnect error, got %v", err)
	}
}

func TestUserServiceConnectUserToChannel_ReactivateExistingMembership(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	user := models.User{DisplayName: "Rejoin"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	channel := models.Channel{
		Code:     "reactivate",
		Name:     "Reactivate",
		MaxUsers: 3,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	leftAt := time.Now().Add(-time.Hour)
	membership := models.ChannelMembership{
		UserID:    user.ID,
		ChannelID: channel.ID,
		Active:    false,
		LeftAt:    &leftAt,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatalf("failed to seed membership: %v", err)
	}

	service := NewUserService()
	if err := service.ConnectUserToChannel(user.ID, "reactivate"); err != nil {
		t.Fatalf("ConnectUserToChannel returned error: %v", err)
	}

	var updatedMembership models.ChannelMembership
	if err := db.First(&updatedMembership, membership.ID).Error; err != nil {
		t.Fatalf("failed to fetch membership: %v", err)
	}
	if !updatedMembership.Active {
		t.Errorf("expected membership reactivated")
	}
	if updatedMembership.LeftAt != nil {
		t.Errorf("expected LeftAt to be nil after activation, got %v", updatedMembership.LeftAt)
	}
}

func TestUserServiceConnectUserToChannel_UserUpdateError(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	user := models.User{DisplayName: "UpdateErr"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	channel := models.Channel{
		Code:     "update-error",
		Name:     "Update Error",
		MaxUsers: 5,
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	forcedErr := errors.New("forced update error")
	failingDB := db.Session(&gorm.Session{NewDB: true})
	failingDB.Callback().Update().Before("gorm:update").Register("force_update_error", func(db *gorm.DB) {
		db.AddError(forcedErr)
	})
	defer failingDB.Callback().Update().Remove("force_update_error")

	service := &UserService{db: failingDB}
	err := service.ConnectUserToChannel(user.ID, "update-error")
	if err == nil || !strings.Contains(err.Error(), "error actualizando usuario") || !strings.Contains(err.Error(), "forced update error") {
		t.Fatalf("expected wrapped update error, got %v", err)
	}
}

func TestUserServiceDisconnectUserFromCurrentChannel(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	channel := models.Channel{
		Code: "canal-disc",
		Name: "Canal Disc",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	user := models.User{
		DisplayName:      "Maria",
		CurrentChannelID: &channel.ID,
		LastActiveAt:     time.Now(),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	membership := models.ChannelMembership{
		UserID:    user.ID,
		ChannelID: channel.ID,
		Active:    true,
	}
	if err := db.Create(&membership).Error; err != nil {
		t.Fatalf("failed to seed membership: %v", err)
	}

	service := NewUserService()
	if err := service.DisconnectUserFromCurrentChannel(user.ID); err != nil {
		t.Fatalf("DisconnectUserFromCurrentChannel returned error: %v", err)
	}

	var updatedUser models.User
	if err := db.First(&updatedUser, user.ID).Error; err != nil {
		t.Fatalf("failed to fetch updated user: %v", err)
	}
	if updatedUser.CurrentChannelID != nil {
		t.Errorf("expected user current channel to be nil, got %v", updatedUser.CurrentChannelID)
	}

	var updatedMembership models.ChannelMembership
	if err := db.First(&updatedMembership, membership.ID).Error; err != nil {
		t.Fatalf("failed to fetch membership: %v", err)
	}
	if updatedMembership.Active {
		t.Errorf("expected membership to be inactive")
	}
	if updatedMembership.LeftAt == nil {
		t.Errorf("expected LeftAt to be set after deactivation")
	}
}

func TestUserServiceGetUserWithChannel(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	channel := models.Channel{
		Code: "canal-a",
		Name: "Canal A",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	user := models.User{
		DisplayName:      "Pedro",
		CurrentChannelID: &channel.ID,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	service := NewUserService()
	result, err := service.GetUserWithChannel(user.ID)
	if err != nil {
		t.Fatalf("GetUserWithChannel returned error: %v", err)
	}
	if result.CurrentChannel == nil || result.CurrentChannel.Code != "canal-a" {
		t.Fatalf("expected user current channel code 'canal-a', got %v", result.CurrentChannel)
	}
}

func TestUserServiceGetUserWithChannel_UserNotFound(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	service := NewUserService()
	_, err := service.GetUserWithChannel(99999)
	if err == nil {
		t.Error("expected error for non-existent user")
	}
	if !strings.Contains(err.Error(), "usuario no encontrado") {
		t.Errorf("expected error to contain 'usuario no encontrado', got %s", err.Error())
	}
}

func TestUserServiceGetChannelActiveUsers(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	channel := models.Channel{
		Code: "canal-users",
		Name: "Canal Users",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("failed to seed channel: %v", err)
	}

	userActive := models.User{DisplayName: "Active"}
	userInactive := models.User{DisplayName: "Inactive"}
	if err := db.Create(&userActive).Error; err != nil {
		t.Fatalf("failed to seed active user: %v", err)
	}
	if err := db.Create(&userInactive).Error; err != nil {
		t.Fatalf("failed to seed inactive user: %v", err)
	}

	memberships := []models.ChannelMembership{
		{UserID: userActive.ID, ChannelID: channel.ID, Active: true},
		{UserID: userInactive.ID, ChannelID: channel.ID, Active: true},
	}
	for i := range memberships {
		if err := db.Create(&memberships[i]).Error; err != nil {
			t.Fatalf("failed to seed membership: %v", err)
		}
	}

	memberships[1].Deactivate()
	if err := db.Save(&memberships[1]).Error; err != nil {
		t.Fatalf("failed to deactivate membership: %v", err)
	}

	service := NewUserService()
	users, err := service.GetChannelActiveUsers("canal-users")
	if err != nil {
		t.Fatalf("GetChannelActiveUsers returned error: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 active user, got %d", len(users))
	}
	if users[0].ID != userActive.ID {
		t.Fatalf("expected active user ID %d, got %d", userActive.ID, users[0].ID)
	}
}

func TestUserServiceGetAvailableChannels(t *testing.T) {
	cleanup := setupUserServiceTestDB(t)
	defer cleanup()

	db := config.DB

	channels := []models.Channel{
		{Code: "public-1", Name: "Public 1", IsPrivate: false},
		{Code: "public-2", Name: "Public 2", IsPrivate: false},
		{Code: "private-1", Name: "Private 1", IsPrivate: true},
	}
	for i := range channels {
		if err := db.Create(&channels[i]).Error; err != nil {
			t.Fatalf("failed to seed channel: %v", err)
		}
	}

	service := NewUserService()
	available, err := service.GetAvailableChannels()
	if err != nil {
		t.Fatalf("GetAvailableChannels returned error: %v", err)
	}
	if len(available) != 2 {
		t.Fatalf("expected 2 public channels, got %d", len(available))
	}

	expected := map[string]bool{
		"public-1": false,
		"public-2": false,
	}
	for _, ch := range available {
		if _, ok := expected[ch.Code]; !ok {
			t.Errorf("unexpected channel code %s", ch.Code)
		} else {
			expected[ch.Code] = true
		}
	}
	for code, found := range expected {
		if !found {
			t.Errorf("expected to find channel %s", code)
		}
	}
}

func TestUserServiceGetAvailableChannels_DBError(t *testing.T) {
	oldDB := config.DB
	defer func() { config.DB = oldDB }()

	// Set DB to a new instance without migrations to simulate error
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	config.DB = db

	service := NewUserService()
	_, err = service.GetAvailableChannels()
	if err == nil {
		t.Error("expected error from DB")
	}
}
