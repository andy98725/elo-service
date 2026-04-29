package models

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type User struct {
	ID            string         `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Username      string         `json:"username" gorm:"uniqueIndex;not null"`
	Email         string         `json:"email" gorm:"uniqueIndex;not null"`
	Password      string         `json:"password" gorm:"not null"`
	CreatedAt     time.Time      `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	IsAdmin       bool           `json:"is_admin" gorm:"default:false"`
	CanCreateGame bool           `json:"can_create_game" gorm:"default:false"`
	// Soft-delete: a non-null DeletedAt hides the row from every default
	// GORM query (login, GetById, etc.), so a deleted user can no longer
	// authenticate or appear in listings. The row stays in the table so
	// match history and ratings keep their FKs intact. Username/email are
	// NOT released for re-registration — soft-deleted accounts continue
	// to occupy their unique-index slot.
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

type UserResp struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	IsAdmin       bool   `json:"is_admin"`
	CanCreateGame bool   `json:"can_create_game"`
}

func (u *User) ToResp() *UserResp {
	return &UserResp{
		ID:            u.ID,
		Username:      u.Username,
		Email:         u.Email,
		IsAdmin:       u.IsAdmin,
		CanCreateGame: u.CanCreateGame,
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

var (
	ErrNotAdmin             = errors.New("admin access required")
	ErrInvalidPassword      = errors.New("current password does not match")
	ErrUsernameTaken        = errors.New("username already taken")
	ErrEmailTaken           = errors.New("email already registered")
)

// UpdateUserProfile changes username and/or email on the given user. Pass
// nil for any field that should stay the same. Returns ErrUsernameTaken
// or ErrEmailTaken on unique-index collision so the handler can map to
// HTTP 409.
//
// TODO: email verification. Right now we trust the client and let the
// caller flip their login email at will. Before any feature gates depend
// on email identity (password reset, ownership transfer, billing, etc.)
// we need to require a confirmation round-trip via an emailed token, and
// gate those features behind the verified flag.
func UpdateUserProfile(userID string, username, email *string) (*User, error) {
	user, err := GetById(userID)
	if err != nil {
		return nil, err
	}
	updates := map[string]interface{}{}
	if username != nil && *username != user.Username {
		updates["username"] = *username
	}
	if email != nil && *email != user.Email {
		updates["email"] = *email
	}
	if len(updates) == 0 {
		return user, nil
	}
	if err := server.S.DB.Model(user).Updates(updates).Error; err != nil {
		msg := err.Error()
		if isUniqueViolation(msg) {
			if _, ok := updates["username"]; ok && strings.Contains(msg, "username") {
				return nil, ErrUsernameTaken
			}
			if _, ok := updates["email"]; ok && strings.Contains(msg, "email") {
				return nil, ErrEmailTaken
			}
		}
		return nil, err
	}
	return user, nil
}

// ChangePassword verifies the user's current password and rotates the
// stored bcrypt hash to a new value. Returns ErrInvalidPassword when the
// supplied current password does not match.
func ChangePassword(userID, currentPassword, newPassword string) error {
	user, err := GetById(userID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(currentPassword)); err != nil {
		return ErrInvalidPassword
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return server.S.DB.Model(user).Update("password", string(hashed)).Error
}

// SoftDeleteUser marks the user deleted via gorm.DeletedAt. After this,
// every default query (login, GetById, etc.) excludes the row, so the
// account can no longer authenticate. FK targets remain intact for
// match/result history.
func SoftDeleteUser(userID string) error {
	return server.S.DB.Where("id = ?", userID).Delete(&User{}).Error
}

func isUniqueViolation(msg string) bool {
	return strings.Contains(msg, "duplicate key value violates unique constraint") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}

// SetCanCreateGame flips the can_create_game flag on the target user. Only an
// admin caller may invoke this — the route guard already enforces this, but
// keeping the check here too means model-level callers can't accidentally
// bypass it.
func SetCanCreateGame(targetID string, value bool, actor *User) (*User, error) {
	if actor == nil || !actor.IsAdmin {
		return nil, ErrNotAdmin
	}
	target, err := GetById(targetID)
	if err != nil {
		return nil, err
	}
	target.CanCreateGame = value
	if err := server.S.DB.Model(target).Update("can_create_game", value).Error; err != nil {
		return nil, err
	}
	return target, nil
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
