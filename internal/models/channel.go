package models

import "gorm.io/gorm"

type Channel struct {
	gorm.Model
	Name      string `gorm:"uniqueIndex"`
	IsPrivate bool
	PinHash   *string
	MaxUsers  int
	CreatorID uint
}
