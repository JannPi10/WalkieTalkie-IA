package models

import "gorm.io/gorm"

type Channel struct {
	gorm.Model
	Code      string              `gorm:"uniqueIndex;not null"`
	Name      string              `gorm:"not null"`
	MaxUsers  int                 `gorm:"default:100"`
	IsPrivate bool                `gorm:"default:false"`
	Members   []ChannelMembership `gorm:"foreignKey:ChannelID"`
}

// GetActiveMembers obtiene los miembros activos del canal
func (c *Channel) GetActiveMembers(db *gorm.DB) ([]ChannelMembership, error) {
	var memberships []ChannelMembership
	err := db.Preload("User").Where("channel_id = ? AND active = ?", c.ID, true).Find(&memberships).Error
	return memberships, err
}

// GetActiveMemberCount obtiene el número de miembros activos
func (c *Channel) GetActiveMemberCount(db *gorm.DB) (int64, error) {
	var count int64
	err := db.Model(&ChannelMembership{}).Where("channel_id = ? AND active = ?", c.ID, true).Count(&count).Error
	return count, err
}
