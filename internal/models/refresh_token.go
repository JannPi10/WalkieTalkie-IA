package models

import (
	"time"

	"gorm.io/gorm"
)

type RefreshToken struct {
	gorm.Model
	UserID    uint
	TokenHash string    `gorm:"index"`
	ExpiresAt time.Time `gorm:"index"`
	RevokedAt *time.Time
	ParentID  *uint
	UserAgent string
	IP        string
}
