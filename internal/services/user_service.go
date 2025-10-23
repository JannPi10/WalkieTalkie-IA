package services

import (
	"fmt"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"gorm.io/gorm"
)

type UserService struct {
	db *gorm.DB
}

func NewUserService() *UserService {
	return &UserService{db: config.DB}
}

// ConnectUserToChannel conecta un usuario a un canal específico
func (s *UserService) ConnectUserToChannel(userID uint, channelCode string) error {
	var channel models.Channel
	if err := s.db.Where("code = ?", channelCode).First(&channel).Error; err != nil {
		return fmt.Errorf("canal no encontrado: %s", channelCode)
	}

	// Verificar capacidad del canal
	activeCount, err := channel.GetActiveMemberCount(s.db)
	if err != nil {
		return fmt.Errorf("error verificando capacidad del canal: %w", err)
	}
	if activeCount >= int64(channel.MaxUsers) {
		return fmt.Errorf("canal lleno: %s", channelCode)
	}

	// Desconectar del canal actual si existe
	if err := s.DisconnectUserFromCurrentChannel(userID); err != nil {
		return fmt.Errorf("error desconectando del canal actual: %w", err)
	}

	// Buscar o crear membresía
	var membership models.ChannelMembership
	err = s.db.Where("user_id = ? AND channel_id = ?", userID, channel.ID).First(&membership).Error
	if err == gorm.ErrRecordNotFound {
		// Crear nueva membresía
		membership = models.ChannelMembership{
			UserID:    userID,
			ChannelID: channel.ID,
			Active:    true,
			JoinedAt:  time.Now(),
		}
		if err := s.db.Create(&membership).Error; err != nil {
			return fmt.Errorf("error creando membresía: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("error buscando membresía: %w", err)
	} else {
		// Activar membresía existente
		membership.Activate()
		if err := s.db.Save(&membership).Error; err != nil {
			return fmt.Errorf("error activando membresía: %w", err)
		}
	}

	// Actualizar usuario
	if err := s.db.Model(&models.User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"current_channel_id": channel.ID,
		"last_active_at":     time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("error actualizando usuario: %w", err)
	}

	return nil
}

// DisconnectUserFromCurrentChannel desconecta al usuario de su canal actual
func (s *UserService) DisconnectUserFromCurrentChannel(userID uint) error {
	var user models.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return fmt.Errorf("usuario no encontrado: %w", err)
	}

	if user.CurrentChannelID == nil {
		return nil // Ya no está en ningún canal
	}

	// Desactivar membresía actual
	var membership models.ChannelMembership
	if err := s.db.Where("user_id = ? AND channel_id = ? AND active = ?", userID, *user.CurrentChannelID, true).First(&membership).Error; err == nil {
		membership.Deactivate()
		if err := s.db.Save(&membership).Error; err != nil {
			return fmt.Errorf("error desactivando membresía: %w", err)
		}
	}

	// Limpiar canal actual del usuario
	if err := s.db.Model(&user).Updates(map[string]interface{}{
		"current_channel_id": nil,
		"last_active_at":     time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("error actualizando usuario: %w", err)
	}

	return nil
}

// GetUserWithChannel obtiene un usuario con su canal actual cargado
func (s *UserService) GetUserWithChannel(userID uint) (*models.User, error) {
	var user models.User
	if err := s.db.Preload("CurrentChannel").First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("usuario no encontrado: %w", err)
	}
	return &user, nil
}

// GetChannelActiveUsers obtiene los usuarios activos de un canal
func (s *UserService) GetChannelActiveUsers(channelCode string) ([]models.User, error) {
	var users []models.User
	err := s.db.Joins("JOIN channel_memberships ON users.id = channel_memberships.user_id").
		Joins("JOIN channels ON channel_memberships.channel_id = channels.id").
		Where("channels.code = ? AND channel_memberships.active = ?", channelCode, true).
		Find(&users).Error
	return users, err
}

// GetAvailableChannels obtiene los canales públicos disponibles
func (s *UserService) GetAvailableChannels() ([]models.Channel, error) {
	var channels []models.Channel
	if err := s.db.Where("is_private = ?", false).Find(&channels).Error; err != nil {
		return nil, fmt.Errorf("error obteniendo canales: %w", err)
	}
	return channels, nil
}
