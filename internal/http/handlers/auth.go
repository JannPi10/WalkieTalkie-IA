package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/response"
	"walkie-backend/pkg/security"
)

var (
	defaultAccessTTL  = 50 * time.Minute
	defaultRefreshTTL = 30 * 24 * time.Hour
)

func Register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FirstName       string `json:"first_name"`
		LastName        string `json:"last_name"`
		Email           string `json:"email"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		response.WriteErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if in.FirstName == "" || in.LastName == "" || in.Email == "" || in.Password == "" || in.ConfirmPassword == "" {
		response.WriteErr(w, http.StatusBadRequest, "Todos los campos son requeridos")
		return
	}
	if in.Password != in.ConfirmPassword {
		response.WriteErr(w, http.StatusBadRequest, "Las contraseñas no coinciden")
		return
	}
	var existing models.User
	if err := config.DB.Where("email = ?", in.Email).First(&existing).Error; err == nil {
		response.WriteErr(w, http.StatusConflict, "El correo ya está registrado")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "Error al procesar contraseña")
		return
	}
	u := models.User{FirstName: in.FirstName, LastName: in.LastName, Email: in.Email, Password: string(hash)}
	if err := config.DB.Create(&u).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "Error al registrar usuario")
		return
	}
	response.WriteJSON(w, http.StatusCreated, map[string]string{"message": "Usuario registrado correctamente"})
}

func Login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		response.WriteErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	in.Password = strings.TrimSpace(in.Password)
	if in.Email == "" || in.Password == "" {
		response.WriteErr(w, http.StatusBadRequest, "Correo y contraseña son requeridos")
		return
	}
	var u models.User
	if err := config.DB.Where("email = ?", in.Email).First(&u).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			response.WriteErr(w, http.StatusUnauthorized, "Correo no registrado")
			return
		}
		response.WriteErr(w, http.StatusInternalServerError, "Error buscando usuario")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(in.Password)) != nil {
		response.WriteErr(w, http.StatusUnauthorized, "Contraseña incorrecta")
		return
	}

	access, err := security.CreateAccessToken(u.ID, u.Email, defaultAccessTTL)
	if err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "Error al generar token")
		return
	}
	plainRefresh, digestRefresh, err := security.GenerateRefreshToken()
	if err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo generar refresh token")
		return
	}
	rt := models.RefreshToken{
		UserID:    u.ID,
		TokenHash: digestRefresh,
		ExpiresAt: time.Now().Add(defaultRefreshTTL),
		UserAgent: r.UserAgent(),
		IP:        r.Header.Get("X-Forwarded-For"),
	}
	if err := config.DB.Create(&rt).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo guardar refresh token")
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]any{
		"accessToken":           access,
		"accessTokenExpiresIn":  int(defaultAccessTTL.Seconds()),
		"refreshToken":          plainRefresh,
		"refreshTokenExpiresAt": rt.ExpiresAt.UTC(),
		"user_id":               u.ID,
		"first_name":            u.FirstName,
		"last_name":             u.LastName,
		"email":                 u.Email,
	})
}
