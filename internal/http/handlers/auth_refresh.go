package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"gorm.io/gorm"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/response"
	"walkie-backend/pkg/security"
)

func Refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refreshToken"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.RefreshToken == "" {
		response.WriteErr(w, http.StatusBadRequest, "refreshToken es requerido")
		return
	}

	digest := security.DigestRefreshToken(in.RefreshToken)
	var rt models.RefreshToken
	if err := config.DB.Where("token_hash = ?", digest).First(&rt).Error; err != nil {
		response.WriteErr(w, http.StatusUnauthorized, "Refresh token inválido")
		return
	}
	now := time.Now()
	if rt.RevokedAt != nil || now.After(rt.ExpiresAt) {
		config.DB.Model(&models.RefreshToken{}).
			Where("user_id = ? AND revoked_at IS NULL", rt.UserID).
			Update("revoked_at", now)
		response.WriteErr(w, http.StatusUnauthorized, "Refresh token inválido o reutilizado")
		return
	}

	var u models.User
	if err := config.DB.First(&u, rt.UserID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			response.WriteErr(w, http.StatusUnauthorized, "Usuario no encontrado")
			return
		}
		response.WriteErr(w, http.StatusInternalServerError, "Error interno")
		return
	}

	// rotar refresh
	nowPtr := now
	if err := config.DB.Model(&rt).Update("revoked_at", &nowPtr).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo rotar refresh token")
		return
	}
	newPlain, newDigest, err := security.GenerateRefreshToken()
	if err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo generar refresh token")
		return
	}
	newRT := models.RefreshToken{
		UserID:    u.ID,
		TokenHash: newDigest,
		ExpiresAt: now.Add(defaultRefreshTTL),
		ParentID:  &rt.ID,
		UserAgent: r.UserAgent(),
		IP:        r.Header.Get("X-Forwarded-For"),
	}
	if err := config.DB.Create(&newRT).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo guardar refresh token")
		return
	}

	access, err := security.CreateAccessToken(u.ID, u.Email, defaultAccessTTL)
	if err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "Error al generar token")
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{
		"accessToken":           access,
		"accessTokenExpiresIn":  int(defaultAccessTTL.Seconds()),
		"refreshToken":          newPlain,
		"refreshTokenExpiresAt": newRT.ExpiresAt.UTC(),
		"message":               "Token renovado correctamente",
	})
}
