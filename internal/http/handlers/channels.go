package handlers

import (
	"net/http"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/response"
)

const PublicMaxUsers = 100

func ListPublicChannels(w http.ResponseWriter, _ *http.Request) {
	var channels []models.Channel
	if err := config.DB.Where("is_private = ?", false).Find(&channels).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo listar canales")
		return
	}

	type item struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		MaxUsers int    `json:"maxUsers"`
	}

	out := make([]item, 0, len(channels))
	for _, ch := range channels {
		out = append(out, item{
			Code:     ch.Code,
			Name:     ch.Name,
			MaxUsers: ch.MaxUsers,
		})
	}
	response.WriteJSON(w, http.StatusOK, out)
}

func ChannelUsers(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("channel")
	if code == "" {
		response.WriteErr(w, http.StatusBadRequest, "Canal inválido")
		return
	}

	var channel models.Channel
	if err := config.DB.Where("code = ?", code).First(&channel).Error; err != nil {
		response.WriteErr(w, http.StatusNotFound, "Canal no encontrado")
		return
	}

	var memberships []models.ChannelMembership
	if err := config.DB.
		Preload("User").
		Where("channel_id = ? AND active = ?", channel.ID, true).
		Find(&memberships).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo obtener los usuarios del canal")
		return
	}

	type member struct {
		ID          uint   `json:"id"`
		DisplayName string `json:"displayName"`
	}

	out := make([]member, 0, len(memberships))
	for _, m := range memberships {
		out = append(out, member{ID: m.UserID, DisplayName: m.User.DisplayName})
	}
	response.WriteJSON(w, http.StatusOK, out)
}
