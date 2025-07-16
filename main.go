package main

import (
	"github.com/joho/godotenv"
	_ "github.com/joho/godotenv"
	"log"
	"net/http"
)

const maxUsuariosPorCanal = 5

var (
	canalesValidos = map[string]bool{
		"canal-1": true,
		"canal-2": true,
		"canal-3": true,
		"canal-4": true,
		"canal-5": true,
	}
)

func main() {
	err := godotenv.Load(".env")
	if err != nil {
		return
	}
	connectDB()

	http.HandleFunc("/register", registerHandler)
	http.HandleFunc("/login", loginHandler)

	http.HandleFunc("/channel-users", channelUsersHandler)
	http.HandleFunc("/channels", listChannelsHandler)
	http.HandleFunc("/ws", handleWebSocket)

	log.Println("Servidor corriendo en http://localhost:8080/ws")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
