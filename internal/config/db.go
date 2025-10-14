package config

import (
	"log"
	"os"

	"walkie-backend/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func ConnectDB() {
	dsn := os.Getenv("DATABASE_URL")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Error connecting PostgreSQL:", err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Channel{},
	); err != nil {
		log.Fatal("Error running migrations:", err)
	}
	DB = db
	log.Println("DB connected & migrated")
}
