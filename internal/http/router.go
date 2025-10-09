package http

import (
	"net/http"
	"walkie-backend/internal/http/handlers"
	"walkie-backend/internal/http/middleware"
)

func Routes(mux *http.ServeMux) {
	// Auth
	mux.HandleFunc("/register", handlers.Register)
	mux.HandleFunc("/login", handlers.Login)
	mux.HandleFunc("/auth/refresh", handlers.Refresh)
	mux.HandleFunc("/auth/logout", handlers.Logout)

	// Channels
	mux.HandleFunc("/channels/public", handlers.ListPublicChannels)
	mux.HandleFunc("/channels/private", middleware.RequireJWT(handlers.ListMyPrivateChannels))
	mux.HandleFunc("/channels/private/create", middleware.RequireJWT(handlers.CreatePrivateChannel))
	mux.HandleFunc("/channels/private/join", middleware.RequireJWT(handlers.JoinPrivateChannel))

	// Users (nombres)
	mux.HandleFunc("/channel-users", handlers.ChannelUsers)

	// WebSocket
	mux.HandleFunc("/ws", middleware.RequireJWT(handlers.HandleWebSocket))
}
