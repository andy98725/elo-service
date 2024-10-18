package models

import "database/sql"

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (u *User) Create(db *sql.DB) error {
	return nil
}

func (u *User) Get() error {
	return nil
}
