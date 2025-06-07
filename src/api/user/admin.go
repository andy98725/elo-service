package user

import (
	"errors"
	"strconv"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

// CreateUser creates the user with the provided username, email, and password.
func CreateUser(ctx echo.Context) error {
	username := ctx.QueryParam("username")
	email := ctx.QueryParam("email")
	password := ctx.QueryParam("password")

	if username == "" || email == "" || password == "" {
		return errors.New("missing required fields")
	}

	user, err := models.Create(models.CreateUserParams{
		Username: username,
		Email:    email,
		Password: password,
	})
	if err != nil {
		return errors.New("error creating user: " + err.Error())
	}

	return ctx.JSON(200, user.ToResp())
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
