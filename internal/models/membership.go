package models

import "gorm.io/gorm"

type ChannelMembership struct {
	gorm.Model
	UserID    uint
	ChannelID uint
}
