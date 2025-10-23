package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"walkie-backend/internal/config"
	httproutes "walkie-backend/internal/http"

	"github.com/joho/godotenv"
)

func main() {
	if err := run(http.ListenAndServe, config.ConnectDB); err != nil {
		log.Fatal(err)
	}
}

func run(listen func(string, http.Handler) error, connectDB func()) error {
	_ = godotenv.Load(".env")

	addr, handler := buildServer(os.Getenv, connectDB, httproutes.Routes)
	log.Println("Server running at http://localhost" + addr)
	return listen(addr, handler)
}

func buildServer(
	getEnv func(string) string,
	connectDB func(),
	registerRoutes func(*http.ServeMux),
) (string, http.Handler) {
	if connectDB != nil {
		connectDB()
	}

	mux := http.NewServeMux()
	if registerRoutes != nil {
		registerRoutes(mux)
	}

	return serverAddress(getEnv), mux
}

func serverAddress(getEnv func(string) string) string {
	port := strings.TrimSpace(getEnv("PORT"))
	if port == "" {
		port = "8080"
	}
	return ":" + port
}
