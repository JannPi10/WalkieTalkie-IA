package handlers

import (
	"net/http"
	"walkie-backend/internal/response"
)

const PublicMaxUsers = 100

var publicChannels = []string{"canal-1", "canal-2", "canal-3", "canal-4", "canal-5"}

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

func ChannelUsers(w http.ResponseWriter, r *http.Request) {
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		response.WriteErr(w, http.StatusBadRequest, "Canal inválido")
		return
	}
	response.WriteJSON(w, http.StatusOK, GetUsersInChannel(channel))
}
