package models

import (
	"database/sql"
	"errors"
	"time"

	"github.com/andy98725/elo-service/src/server"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"created_at"`
	IsAdmin   bool      `json:"is_admin"`
}

type CreateUserParams struct {
	Username string
	Email    string
	Password string
}

func Create(params CreateUserParams) (*User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &User{}
	err = server.S.DB.QueryRow(`
		INSERT INTO users (username, email, password)
		VALUES ($1, $2, $3)
		RETURNING id, username, email, password, created_at, is_admin`,
		params.Username, params.Email, hashedPassword).Scan(
		&user.ID, &user.Username, &user.Email, &user.Password, &user.CreatedAt, &user.IsAdmin)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func GetByUsername(username string) (*User, error) {
	u := &User{}
	err := server.S.DB.QueryRow(`
		SELECT id, username, password, created_at 
		FROM users 
		WHERE username = $1`,
		username).Scan(&u.ID, &u.Username, &u.Password, &u.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("invalid username")
		}
		return nil, err
	}
	return u, nil
}

func GetByEmail(email string) (*User, error) {
	u := &User{}
	err := server.S.DB.QueryRow(`
		SELECT id, username, password, created_at 
		FROM users 
		WHERE email = $1`,
		email).Scan(&u.ID, &u.Username, &u.Password, &u.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("invalid email")
		}
		return nil, err
	}
	return u, nil
}

func GetById(id string) (*User, error) {
	u := &User{}
	err := server.S.DB.QueryRow(`
		SELECT id, username, password, created_at 
		FROM users 
		WHERE id = $1`,
		id).Scan(&u.ID, &u.Username, &u.Password, &u.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.New("invalid user id")
		}
		return nil, err
	}
	return u, nil
}
