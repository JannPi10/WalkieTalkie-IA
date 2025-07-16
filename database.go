package main

import (
	"log"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"walkie-backend/models"
)

var db *gorm.DB

func connectDB() {
	dsn := os.Getenv("DATABASE_URL")
	var err error

	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("❌ Error conectando a PostgreSQL:", err)
	}

	// Auto migración de tablas
	if err := db.AutoMigrate(&models.User{}); err != nil {
		log.Fatal("❌ Error en migración:", err)
	}

	log.Println("📦 Conectado a PostgreSQL y migrado")
}
