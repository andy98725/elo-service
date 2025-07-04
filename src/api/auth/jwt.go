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
	JWT_TIMEOUT             = time.Hour * 24
)

type UserClaims struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username"`
	jwt.StandardClaims
}

func Login(email, displayName, password string) (string, *models.User, error) {
	user, err := models.GetByEmail(email)
	if err != nil {
		slog.Warn("Invalid email", "error", err)
		return "", nil, errors.New(ERR_INVALID_CREDENTIALS)
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		slog.Warn("Invalid password", "error", err)
		return "", nil, errors.New(ERR_INVALID_CREDENTIALS)
	}

	if displayName == "" {
		displayName = user.Username
	}

	claims := &UserClaims{
		UserID:      user.ID,
		DisplayName: displayName,
		Username:    user.Username,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(JWT_TIMEOUT).Unix(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(jwtKey)
	if err != nil {
		return "", nil, err
	}
	return signedToken, user, nil

}

type GuestClaims struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	jwt.StandardClaims
}

func GuestLogin(displayName string) (string, *GuestClaims, error) {
	claims := &GuestClaims{
		ID:          "g_" + uuid.New().String(),
		DisplayName: displayName,
		StandardClaims: jwt.StandardClaims{
			ExpiresAt: time.Now().Add(JWT_TIMEOUT).Unix(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(jwtKey)
	if err != nil {
		return "", nil, err
	}
	return signedToken, claims, nil
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

	if claims.UserID == "" {
		return nil, errors.New("missing user ID")
	}
	if claims.DisplayName == "" {
		return nil, errors.New("missing display name")
	}
	if claims.Username == "" {
		return nil, errors.New("missing username")
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

	if claims.ID == "" {
		return nil, errors.New("missing guest ID")
	}
	if claims.DisplayName == "" {
		return nil, errors.New("missing display name")
	}

	return claims, nil
}
