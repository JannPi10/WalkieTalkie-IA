package http

import (
	"net/http"
	"walkie-backend/internal/http/handlers"
)

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("/channels/public", handlers.ListPublicChannels)
	mux.HandleFunc("/channel-users", handlers.ChannelUsers)
	mux.HandleFunc("/ws", handlers.HandleWebSocket)
}
