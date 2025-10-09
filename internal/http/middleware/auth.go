package middleware

import (
	"net/http"
	"walkie-backend/internal/response"
	"walkie-backend/pkg/security"
)

func RequireJWT(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, err := security.ParseFromHeader(r)
		if err != nil || !tok.Valid {
			response.WriteErr(w, http.StatusUnauthorized, "Token inválido")
			return
		}
		next.ServeHTTP(w, r)
	}
}
