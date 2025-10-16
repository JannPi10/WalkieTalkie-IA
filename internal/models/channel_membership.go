package models

import (
	"time"

	"gorm.io/gorm"
)

type ChannelMembership struct {
	gorm.Model
	UserID    uint `gorm:"index"`
	User      User
	ChannelID uint `gorm:"index"`
	Channel   Channel
	Active    bool
	JoinedAt  time.Time
	LeftAt    *time.Time
}
