package main

import (
	"log"
	"net/http"
	"os"

	httproutes "walkie-backend/internal/http"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load(".env")

	mux := http.NewServeMux()
	httproutes.Routes(mux)

	addr := ":8080"
	if v := os.Getenv("PORT"); v != "" {
		addr = ":" + v
	}
	log.Println("Server running at http://localhost" + addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
