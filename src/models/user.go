package models

import (
	"errors"
	"log/slog"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Username  string    `json:"username" gorm:"uniqueIndex;not null"`
	Email     string    `json:"email" gorm:"uniqueIndex;not null"`
	Password  string    `json:"password" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	IsAdmin   bool      `json:"is_admin" gorm:"default:false"`
}

type UserResp struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	IsAdmin  bool   `json:"is_admin"`
}

func (u *User) ToResp() *UserResp {
	return &UserResp{
		ID:       u.ID,
		Username: u.Username,
		Email:    u.Email,
		IsAdmin:  u.IsAdmin,
	}
}

type Guest struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type CreateUserParams struct {
	Username string
	Email    string
	Password string
}

func CreateUser(params CreateUserParams) (*User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &User{
		Username: params.Username,
		Email:    params.Email,
		Password: string(hashedPassword),
	}

	result := server.S.DB.Create(user)
	if result.Error != nil {
		return nil, result.Error
	}

	return user, nil
}

func GetUsers(page, pageSize int) ([]User, int, error) {
	var users []User
	offset := page * pageSize
	result := server.S.DB.Offset(offset).Limit(pageSize).Find(&users)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return users, nextPage, result.Error
}

func GetByUsername(username string) (*User, error) {
	var user User
	result := server.S.DB.Where("username = ?", username).First(&user)
	return &user, result.Error
}

func GetByEmail(email string) (*User, error) {
	var user User
	result := server.S.DB.Where("email = ?", email).First(&user)
	slog.Info("User", "user", user)
	return &user, result.Error
}

func GetById(id string) (*User, error) {
	var user User
	result := server.S.DB.First(&user, "id = ?", id)
	return &user, result.Error
}

func UserIDFromContext(ctx echo.Context) (string, error) {
	user := ctx.Get("user").(*User)
	if user == nil {
		return "", errors.New("user not found in context")
	}

	if user.IsAdmin && ctx.QueryParam("id") != "" {
		return ctx.QueryParam("id"), nil
	}

	return user.ID, nil
}
