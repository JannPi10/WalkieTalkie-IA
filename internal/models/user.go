package models

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	DisplayName      string `gorm:"uniqueIndex"`
	Email            string `gorm:"uniqueIndex"`
	CurrentChannelID *uint  `gorm:"index"`
	CurrentChannel   *Channel
	LastActiveAt     time.Time
	Memberships      []ChannelMembership
}
