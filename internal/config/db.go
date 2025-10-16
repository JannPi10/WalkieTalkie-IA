package config

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"walkie-backend/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
	DB   *gorm.DB
	once sync.Once
)

func ConnectDB() {
	once.Do(func() {
		dsn := os.Getenv("DATABASE_URL")
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err != nil {
			log.Fatal("Error connecting PostgreSQL:", err)
		}

		if err := db.AutoMigrate(
			&models.User{},
			&models.Channel{},
			&models.ChannelMembership{},
		); err != nil {
			log.Fatal("Error running migrations:", err)
		}

		seedDatabase(db)
		DB = db
		log.Println("DB connected, migrated and seeded")
	})
}

func seedDatabase(db *gorm.DB) {
	channels := []models.Channel{
		{Code: "canal-1", Name: "Canal 1", MaxUsers: 100},
		{Code: "canal-2", Name: "Canal 2", MaxUsers: 100},
		{Code: "canal-3", Name: "Canal 3", MaxUsers: 100},
		{Code: "canal-4", Name: "Canal 4", MaxUsers: 100},
		{Code: "canal-5", Name: "Canal 5", MaxUsers: 100},
	}
	for _, ch := range channels {
		var count int64
		db.Model(&models.Channel{}).Where("code = ?", ch.Code).Count(&count)
		if count == 0 {
			if err := db.Create(&ch).Error; err != nil {
				log.Printf("seed channel %s: %v", ch.Code, err)
			}
		}
	}

	for i := 1; i <= 10; i++ {
		display := fmt.Sprintf("usuario-%02d", i)
		email := fmt.Sprintf("%s@example.com", display)
		var count int64
		db.Model(&models.User{}).Where("display_name = ?", display).Count(&count)
		if count == 0 {
			user := models.User{
				DisplayName:  display,
				Email:        email,
				LastActiveAt: time.Now(),
			}
			if err := db.Create(&user).Error; err != nil {
				log.Printf("seed user %s: %v", display, err)
			}
		}
	}
}
