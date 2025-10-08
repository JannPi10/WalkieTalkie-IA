package main

import (
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"os"
	"strings"
	"time"
	"walkie-backend/models"
)

func registerHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		FirstName       string `json:"first_name"`
		LastName        string `json:"last_name"`
		Email           string `json:"email"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirm_password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	// Validaciones básicas
	if input.FirstName == "" || input.LastName == "" || input.Email == "" ||
		input.Password == "" || input.ConfirmPassword == "" {
		writeJSONError(w, http.StatusBadRequest, "Todos los campos son requeridos")
		return
	}

	if input.Password != input.ConfirmPassword {
		writeJSONError(w, http.StatusBadRequest, "Las contraseñas no coinciden")
		return
	}

	// ✅ SOLUCIÓN: Verificar si el email ya existe ANTES de intentar crear el usuario
	var existingUser models.User
	if err := db.Where("email = ?", input.Email).First(&existingUser).Error; err == nil {
		// El usuario existe (err == nil significa que se encontró)
		writeJSONError(w, http.StatusConflict, "El correo ya está registrado")
		return
	}

	// Hashear la contraseña
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error al procesar contraseña")
		return
	}

	// Crear el usuario
	user := models.User{
		FirstName: input.FirstName,
		LastName:  input.LastName,
		Email:     input.Email,
		Password:  string(hash),
	}

	if err := db.Create(&user).Error; err != nil {
		// Como verificación de respaldo por si acaso hay race condition
		if strings.Contains(err.Error(), "duplicate key") {
			writeJSONError(w, http.StatusConflict, "El correo ya está registrado")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "Error al registrar usuario")
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"message": "Usuario registrado correctamente",
	})
}

var jwtSecret = []byte(os.Getenv("JWT_SECRET"))

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	input.Email = strings.TrimSpace(input.Email)
	input.Password = strings.TrimSpace(input.Password)

	if input.Email == "" || input.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "Correo y contraseña son requeridos")
		return
	}

	// Buscar usuario por email
	var user models.User
	if err := db.Where("email = ?", input.Email).First(&user).Error; err != nil {
		writeJSONError(w, http.StatusUnauthorized, "Correo no registrado")
		return
	}

	// Verificar contraseña
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.Password)); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "Contraseña incorrecta")
		return
	}

	// Crear token JWT
	claims := jwt.MapClaims{
		"user_id": user.ID,
		"email":   user.Email,
		"exp":     time.Now().Add(24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signed, err := token.SignedString(jwtSecret)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Error al generar token")
		return
	}

	// Respuesta con token y nombre
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":      signed,
		"user_id":    user.ID,
		"first_name": user.FirstName,
		"last_name":  user.LastName,
		"email":      user.Email,
	})
}
