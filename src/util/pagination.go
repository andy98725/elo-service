package util

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo"
)

func ParsePagination(ctx echo.Context) (int, int, error) {
	page := ctx.QueryParam("page")
	if page == "" {
		page = "0"
	}
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		return 0, 0, echo.NewHTTPError(http.StatusBadRequest, "invalid page param")
	}

	pageSize := ctx.QueryParam("pageSize")
	if pageSize == "" {
		pageSize = "10"
	}
	pageSizeInt, err := strconv.Atoi(pageSize)
	if err != nil {
		return 0, 0, echo.NewHTTPError(http.StatusBadRequest, "invalid pageSize param")
	}

	return pageInt, pageSizeInt, nil
}
