package httphandler

import (
	"net/http"

	"walkie-backend/internal/httpHandler/handlers"
)

func Routes(mux *http.ServeMux) {
	mux.HandleFunc("/channels/public", handlers.ListPublicChannels)
	mux.HandleFunc("/channel-users", handlers.ChannelUsers)
	mux.HandleFunc("/ws", handlers.HandleWebSocket)
	mux.HandleFunc("/audio/ingest", handlers.AudioIngest)
	mux.HandleFunc("/audio/poll", handlers.AudioPoll)
	mux.HandleFunc("/auth", handlers.Authenticate)
}
