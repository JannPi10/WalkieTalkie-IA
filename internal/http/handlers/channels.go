package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"walkie-backend/internal/config"
	"walkie-backend/internal/models"
	"walkie-backend/internal/response"
	"walkie-backend/pkg/security"
)

const (
	PublicMaxUsers  = 100
	PrivateMaxUsers = 15
)

// No se prellenan privados. Públicos (si deseas mantenerlos visibles globalmente):
var publicChannels = []string{"canal-1", "canal-2", "canal-3", "canal-4", "canal-5"}

var channelAllowed = regexp.MustCompile(`^[A-Za-z0-9_-]{1,13}$`)
var channelHasDigit = regexp.MustCompile(`[0-9]`)

func isValidChannelName(name string) bool {
	if !channelAllowed.MatchString(name) {
		return false
	}
	if !channelHasDigit.MatchString(name) {
		return false
	}
	return true
}

// GET /channels/public  -> lista global de públicos (isPrivate:false)
func ListPublicChannels(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Name      string `json:"name"`
		IsPrivate bool   `json:"isPrivate"`
		MaxUsers  int    `json:"maxUsers"`
	}
	out := make([]item, 0, len(publicChannels))
	for _, name := range publicChannels {
		out = append(out, item{Name: name, IsPrivate: false, MaxUsers: PublicMaxUsers})
	}
	response.WriteJSON(w, http.StatusOK, out)
}

// GET /channels/private (JWT) -> SOLO privados del usuario (inicialmente [])
func ListMyPrivateChannels(w http.ResponseWriter, r *http.Request) {
	userID, _, err := security.MustUserIDEmail(r)
	if err != nil {
		response.WriteErr(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	type item struct {
		Name      string `json:"name"`
		IsPrivate bool   `json:"isPrivate"`
		MaxUsers  int    `json:"maxUsers"`
	}

	channels := make([]item, 0) // <- clave: no nil

	err = config.DB.Table("channels").
		Select("channels.name AS name, channels.is_private AS is_private, channels.max_users AS max_users").
		Joins("JOIN channel_memberships ON channel_memberships.channel_id = channels.id").
		Where("channel_memberships.user_id = ? AND channels.is_private = TRUE", userID).
		Scan(&channels).Error
	if err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudieron listar los canales")
		return
	}
	response.WriteJSON(w, http.StatusOK, channels) // ahora envía [] en vez de null
}

// POST /channels/private/create  { "name":"FESC-1", "pin":"1234" }  (JWT)
// Crea canal privado y agrega membership (para que aparezca en la lista de privados).
func CreatePrivateChannel(w http.ResponseWriter, r *http.Request) {
	userID, _, err := security.MustUserIDEmail(r)
	if err != nil {
		response.WriteErr(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	var in struct {
		Name string `json:"name"`
		PIN  string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		response.WriteErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.PIN = strings.TrimSpace(in.PIN)
	if !isValidChannelName(in.Name) {
		response.WriteErr(w, http.StatusBadRequest, "Nombre inválido (máximo 13 caracteres y al menos un número)")
		return
	}
	if in.PIN == "" {
		response.WriteErr(w, http.StatusBadRequest, "PIN es requerido")
		return
	}
	// evitar colisión con públicos
	for _, p := range publicChannels {
		if strings.EqualFold(p, in.Name) {
			response.WriteErr(w, http.StatusConflict, "Ese nombre ya existe como canal público")
			return
		}
	}
	// evitar duplicado privado
	var existing models.Channel
	if err := config.DB.Where("name = ?", in.Name).First(&existing).Error; err == nil {
		response.WriteErr(w, http.StatusConflict, "Ya existe un canal con ese nombre")
		return
	}
	hash, err := security.HashPIN(in.PIN)
	if err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo hashear el PIN")
		return
	}
	ch := models.Channel{
		Name:      in.Name,
		IsPrivate: true,
		PinHash:   &hash,
		MaxUsers:  PrivateMaxUsers,
		CreatorID: userID,
	}
	if err := config.DB.Create(&ch).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo crear el canal")
		return
	}
	// membership para que ya aparezca
	if err := config.DB.Create(&models.ChannelMembership{UserID: userID, ChannelID: ch.ID}).Error; err != nil {
		response.WriteErr(w, http.StatusInternalServerError, "No se pudo registrar la membresía")
		return
	}
	response.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":        ch.ID,
		"name":      ch.Name,
		"isPrivate": ch.IsPrivate,
		"maxUsers":  ch.MaxUsers,
		"message":   "Canal creado y unido correctamente",
	})
}

// POST /channels/private/join  { "name":"FESC-1", "pin":"1234" }  (JWT)
func JoinPrivateChannel(w http.ResponseWriter, r *http.Request) {
	userID, _, err := security.MustUserIDEmail(r)
	if err != nil {
		response.WriteErr(w, http.StatusUnauthorized, "Usuario no autenticado")
		return
	}
	var in struct {
		Name string `json:"name"`
		PIN  string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		response.WriteErr(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.PIN = strings.TrimSpace(in.PIN)
	if in.Name == "" || in.PIN == "" {
		response.WriteErr(w, http.StatusBadRequest, "Nombre y PIN son requeridos")
		return
	}
	var ch models.Channel
	if err := config.DB.Where("name = ? AND is_private = TRUE", in.Name).First(&ch).Error; err != nil {
		response.WriteErr(w, http.StatusNotFound, "Canal no encontrado")
		return
	}
	if ch.PinHash == nil || !security.CheckPIN(*ch.PinHash, in.PIN) {
		response.WriteErr(w, http.StatusUnauthorized, "PIN incorrecto")
		return
	}

	// idempotente
	var count int64
	config.DB.Model(&models.ChannelMembership{}).
		Where("user_id = ? AND channel_id = ?", userID, ch.ID).
		Count(&count)
	if count == 0 {
		if err := config.DB.Create(&models.ChannelMembership{UserID: userID, ChannelID: ch.ID}).Error; err != nil {
			response.WriteErr(w, http.StatusInternalServerError, "No se pudo unir al canal")
			return
		}
	}
	response.WriteJSON(w, http.StatusOK, map[string]any{
		"id":        ch.ID,
		"name":      ch.Name,
		"isPrivate": ch.IsPrivate,
		"maxUsers":  ch.MaxUsers,
		"message":   "Unido al canal correctamente",
	})
}

// GET /channel-users?channel=FESC-1   -> devuelve nombres ["Ana Pérez", ...]
func ChannelUsers(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		response.WriteErr(w, http.StatusBadRequest, "Canal inválido")
		return
	}
	users := GetUsersInChannel(channel) // implementado en ws.go
	response.WriteJSON(w, http.StatusOK, users)
}
