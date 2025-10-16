package models

import "gorm.io/gorm"

type Channel struct {
	gorm.Model
	Code      string `gorm:"uniqueIndex"`
	Name      string
	MaxUsers  int
	IsPrivate bool
	Members   []ChannelMembership
}
