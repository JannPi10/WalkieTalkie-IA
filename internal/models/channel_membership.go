package models

import (
	"time"

	"gorm.io/gorm"
)

type ChannelMembership struct {
	gorm.Model
	UserID    uint      `gorm:"index;not null"`
	User      User      `gorm:"foreignKey:UserID"`
	ChannelID uint      `gorm:"index;not null"`
	Channel   Channel   `gorm:"foreignKey:ChannelID"`
	Active    bool      `gorm:"default:true;index"`
	JoinedAt  time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	LeftAt    *time.Time
}

// Activate marca la membresía como activa
func (cm *ChannelMembership) Activate() {
	cm.Active = true
	cm.LeftAt = nil
}

// Deactivate marca la membresía como inactiva
func (cm *ChannelMembership) Deactivate() {
	cm.Active = false
	now := time.Now()
	cm.LeftAt = &now
}
