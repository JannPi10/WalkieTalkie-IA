package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"

	"golang.org/x/crypto/bcrypt"
)

// AuthenticationRequest is the expected request body from mobile
// {"nombre":"...","pin":1234}  // pin ahora es int
type AuthenticationRequest struct {
	Nombre string `json:"nombre"`
	Pin    int    `json:"pin"`
}

// AuthenticationResponse is the JSON response
// {"message":"usuario registrado exitosamente","token":"..."}
type AuthenticationResponse struct {
	Message string `json:"message"`
	Token   string `json:"token"`
}

// Authenticate handles POST /auth
// - On success: 200, Content-Type: application/json, body: {"message":"usuario registrado exitosamente","token":"..."}
// - On invalid: 401 application/json {"message":"credenciales inválidas"}
func Authenticate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"message":"método no permitido"}`, http.StatusMethodNotAllowed)
		return
	}

	var req AuthenticationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"message":"JSON inválido"}`, http.StatusBadRequest)
		return
	}
	req.Nombre = strings.TrimSpace(req.Nombre)
	if req.Nombre == "" || req.Pin <= 0 {
		http.Error(w, `{"message":"nombre y pin son requeridos"}`, http.StatusBadRequest)
		return
	}

	// Find or create user by DisplayName
	var user models.User
	if err := config.DB.Where("display_name = ?", req.Nombre).First(&user).Error; err != nil {
		// Create new user
		email := fmt.Sprintf("%s@local", slugify(req.Nombre))
		pinHash, _ := bcrypt.GenerateFromPassword([]byte(fmt.Sprintf("%d", req.Pin)), bcrypt.DefaultCost)
		user = models.User{
			DisplayName:  req.Nombre,
			Email:        email,
			IsActive:     true,
			LastActiveAt: time.Now(),
			PinHash:      string(pinHash),
		}
		if err := config.DB.Create(&user).Error; err != nil {
			http.Error(w, `{"message":"no se pudo registrar usuario"}`, http.StatusInternalServerError)
			return
		}
	} else {
		// User exists: validate pin (if previously set)
		if user.PinHash != "" {
			if err := bcrypt.CompareHashAndPassword([]byte(user.PinHash), []byte(fmt.Sprintf("%d", req.Pin))); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(AuthenticationResponse{Message: "credenciales inválidas"})
				return
			}
		} else {
			// First-time set pin
			pinHash, _ := bcrypt.GenerateFromPassword([]byte(fmt.Sprintf("%d", req.Pin)), bcrypt.DefaultCost)
			user.PinHash = string(pinHash)
		}
		user.IsActive = true
		user.LastActiveAt = time.Now()
		_ = config.DB.Save(&user).Error
	}

	// Generate and store auth token
	token, err := generateToken(32)
	if err != nil {
		http.Error(w, `{"message":"no se pudo generar token"}`, http.StatusInternalServerError)
		return
	}
	user.AuthToken = token
	user.LastActiveAt = time.Now()
	if err := config.DB.Save(&user).Error; err != nil {
		http.Error(w, `{"message":"no se pudo guardar token"}`, http.StatusInternalServerError)
		return
	}

	// Respond JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(AuthenticationResponse{
		Message: "usuario registrado exitosamente",
		Token:   token,
	})
}

func generateToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9\.]+`)

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", ".")
	s = nonAlnum.ReplaceAllString(s, "")
	if s == "" {
		return fmt.Sprintf("user.%d", time.Now().Unix())
	}
	return s
}
