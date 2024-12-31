package user

import (
	"errors"
	"strconv"

	"github.com/labstack/echo"
)

// CreateUser creates the user with the provided username, email, and password.
// Requires admin.
func CreateUser(ctx echo.Context) error {
	//TODO
	return nil
}

// GetUser finds the user matching the provided ID.
// Requires admin.
func GetUser(ctx echo.Context) error {
	idStr := ctx.QueryParam("id")
	if idStr == "" {
		return errors.New("missing param id")
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return errors.New("invalid id param")
	}

	//TODO find user

	return ctx.JSON(200, struct{ id int }{id: id})
}

// UpdateUser updates the user's username, email, and password
// matching the provided ID.
// Requires admin.
// Returns the user struct.
func UpdateUser(ctx echo.Context) error {
	//TODO
	return nil
}

func DeleteUser(ctx echo.Context) error {
	//TODO
	return nil
}

func GetUsers(ctx echo.Context) error {
	//TODO
	return nil
}
