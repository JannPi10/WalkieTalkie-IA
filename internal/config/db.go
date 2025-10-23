package config

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"walkie-backend/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	DB   *gorm.DB
	once sync.Once
)

func ConnectDB() {
	once.Do(func() {
		db, err := connectAndMigrate(os.Getenv("DATABASE_URL"))
		if err != nil {
			log.Fatal("Error connecting PostgreSQL:", err)
		}
		DB = db
		log.Println("DB connected, migrated and seeded")
	})
}

func connectAndMigrate(dsn string) (*gorm.DB, error) {
	var dialector gorm.Dialector
	if dsn == ":memory:" || strings.HasPrefix(dsn, "file::") {
		dialector = sqlite.Open(dsn)
	} else {
		dialector = postgres.Open(dsn)
	}

	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(
		&models.User{},
		&models.Channel{},
		&models.ChannelMembership{},
	); err != nil {
		return nil, err
	}

	seedDatabase(db)
	return db, nil
}

func seedDatabase(db *gorm.DB) {
	channels := []models.Channel{
		{Code: "canal-1", Name: "Canal 1", MaxUsers: 100, IsPrivate: false},
		{Code: "canal-2", Name: "Canal 2", MaxUsers: 100, IsPrivate: false},
		{Code: "canal-3", Name: "Canal 3", MaxUsers: 100, IsPrivate: false},
		{Code: "canal-4", Name: "Canal 4", MaxUsers: 100, IsPrivate: false},
		{Code: "canal-5", Name: "Canal 5", MaxUsers: 100, IsPrivate: false},
	}

	for _, ch := range channels {
		var count int64
		db.Model(&models.Channel{}).Where("code = ?", ch.Code).Count(&count)
		if count == 0 {
			if err := db.Create(&ch).Error; err != nil {
				log.Printf("Error seeding channel %s: %v", ch.Code, err)
			} else {
				log.Printf("Canal creado: %s", ch.Code)
			}
		}
	}

	for i := 1; i <= 10; i++ {
		displayName := fmt.Sprintf("usuario-%02d", i)

		var count int64
		db.Model(&models.User{}).Where("display_name = ?", displayName).Count(&count)
		if count == 0 {
			user := models.User{
				DisplayName:  displayName,
				IsActive:     true,
				LastActiveAt: time.Now(),
			}
			if err := db.Create(&user).Error; err != nil {
				log.Printf("Error seeding user %s: %v", displayName, err)
			} else {
				log.Printf("Usuario creado: %s (ID: %d)", displayName, user.ID)
			}
		}
	}

	log.Println("Database seeding completed")
}
