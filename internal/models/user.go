package models

import (
	"time"

	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	DisplayName      string   `gorm:"uniqueIndex;not null"`
	Email            string   `gorm:"uniqueIndex;not null"`
	CurrentChannelID *uint    `gorm:"index"`
	CurrentChannel   *Channel `gorm:"foreignKey:CurrentChannelID"`
	IsActive         bool     `gorm:"default:true"`
	LastActiveAt     time.Time
	Memberships      []ChannelMembership `gorm:"foreignKey:UserID"`
}

// IsInChannel verifica si el usuario está actualmente en un canal
func (u *User) IsInChannel() bool {
	return u.CurrentChannelID != nil
}

// GetCurrentChannelCode obtiene el código del canal actual
func (u *User) GetCurrentChannelCode() string {
	if u.CurrentChannel != nil {
		return u.CurrentChannel.Code
	}
	return ""
}
