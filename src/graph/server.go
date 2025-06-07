package graph

import (
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/labstack/echo"
)

func GraphQLHandler() echo.HandlerFunc {
	h := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{}}))

	return func(c echo.Context) error {
		h.ServeHTTP(c.Response().Writer, c.Request())
		return nil
	}
}

func PlaygroundHandler() echo.HandlerFunc {
	h := playground.Handler("GraphQL playground", "/query")

	return func(c echo.Context) error {
		h.ServeHTTP(c.Response().Writer, c.Request())
		return nil
	}
}
