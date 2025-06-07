package auth

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/andy98725/elo-service/src/models"

	"github.com/golang-jwt/jwt"
	"golang.org/x/crypto/bcrypt"
)

var jwtKey = []byte(os.Getenv("JWT_SECRET_KEY"))

const (
	ERR_INVALID_CREDENTIALS = "invalid email or password"
)

type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	jwt.StandardClaims
}

func Login(email, password string) (string, error) {
	user, err := models.GetByEmail(email)
	if err != nil {
		slog.Warn("Invalid email", "error", err)
		return "", errors.New(ERR_INVALID_CREDENTIALS)
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		slog.Warn("Invalid password", "error", err)
		return "", errors.New(ERR_INVALID_CREDENTIALS)
	}

	token, err := generateToken(user.ID, user.Username)
	if err != nil {
		slog.Error("Token generation failed", "error", err)
		return "", err
	}

	return token, nil
}

func generateToken(userID, username string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Username: username,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(time.Hour * 24).Unix(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

func ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return jwtKey, nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, jwt.ErrSignatureInvalid
	}

	return claims, nil
}
