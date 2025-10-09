package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/response"
	"walkie-backend/pkg/security"
)

func Logout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refreshToken"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	now := time.Now()
	if in.RefreshToken != "" {
		d := security.DigestRefreshToken(in.RefreshToken)
		config.DB.Model(&models.RefreshToken{}).
			Where("token_hash = ? AND revoked_at IS NULL", d).
			Update("revoked_at", now)
	} else if uid, _, err := security.MustUserIDEmail(r); err == nil {
		config.DB.Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", uid).
			Update("revoked_at", now)
	}
	response.WriteJSON(w, http.StatusOK, map[string]string{"message": "Sesión cerrada correctamente"})
}
