package auth

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/andy98725/elo-service/src/models"
	"github.com/google/uuid"

	"github.com/golang-jwt/jwt"
	"golang.org/x/crypto/bcrypt"
)

var jwtKey = []byte(os.Getenv("JWT_SECRET_KEY"))

const (
	ERR_INVALID_CREDENTIALS = "invalid email or password"
)

type UserClaims struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username"`
	jwt.StandardClaims
}

func Login(email, displayName, password string) (string, error) {
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

	claims := &UserClaims{
		UserID:      user.ID,
		DisplayName: displayName,
		Username:    user.Username,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(time.Hour * 24).Unix(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)

}

type GuestClaims struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	jwt.StandardClaims
}

func GuestLogin(displayName string) (string, error) {
	claims := &GuestClaims{
		ID:          uuid.New().String(),
		DisplayName: displayName,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(time.Hour * 24).Unix(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

func ValidateUserToken(tokenString string) (*UserClaims, error) {
	claims := &UserClaims{}
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
func ValidateGuestToken(tokenString string) (*GuestClaims, error) {
	claims := &GuestClaims{}
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
