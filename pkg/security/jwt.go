package security

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var jwtSecret = []byte(os.Getenv("JWT_SECRET"))

func CreateAccessToken(userID uint, email string, ttl time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"email":   email,
		"exp":     time.Now().Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

func ParseFromHeader(r *http.Request) (*jwt.Token, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
		return nil, jwt.ErrTokenMalformed
	}
	tokenStr := strings.TrimPrefix(auth, "Bearer ")
	return jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) { return jwtSecret, nil })
}

func MustUserIDEmail(r *http.Request) (uint, string, error) {
	tok, err := ParseFromHeader(r)
	if err != nil || !tok.Valid {
		return 0, "", jwt.ErrTokenInvalidClaims
	}
	claims := tok.Claims.(jwt.MapClaims)
	uidF, _ := claims["user_id"].(float64)
	email, _ := claims["email"].(string)
	return uint(uidF), email, nil
}
