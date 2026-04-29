package match

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/andy98725/elo-service/src/external/aws"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// MaxArtifactBytes caps a single artifact's body. 1 MiB is generous for
// previews and game-defined replay files; oversized payloads belong in
// the spectator stream pipeline.
const MaxArtifactBytes = 1 << 20

// MaxArtifactsPerMatch caps how many distinct artifact names one match
// can carry. Bounds storage abuse from a leaked auth_code.
const MaxArtifactsPerMatch = 10

// artifactNamePattern mirrors the playerData key rule. Forbids slashes,
// dots-only, leading dashes, etc. — anything that could escape the
// artifacts/<matchID>/<name> path.
var artifactNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// UploadMatchArtifact godoc
// @Summary      Upload a named artifact for the active match
// @Description  Game server uploads opaque bytes (replay file, preview image, highlight reel, etc.) for the active match. Auth is the match auth_code carried as Authorization: Bearer <code>. Name must match [a-zA-Z0-9._-]{1,64}; uploading the same name again overwrites. Up to 10 distinct names per match. Body is capped at 1 MiB; Content-Type is preserved and returned on download. The platform doesn't interpret the bytes — `preview` and `replay` are conventional names that generic UIs may render but no validation is performed on shape.
// @Tags         Matches
// @Accept       application/octet-stream
// @Produce      json
// @Security     BearerAuth
// @Param        name query string true "Artifact name (a-zA-Z0-9._-, max 64 chars)"
// @Success      200 {object} map[string]interface{} "name, size_bytes, content_type"
// @Failure      400 {object} echo.HTTPError "invalid name or too many artifacts"
// @Failure      401 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError "match is not underway"
// @Failure      413 {object} echo.HTTPError "artifact exceeds 1 MiB"
// @Failure      500 {object} echo.HTTPError
// @Router       /match/artifact [post]
func UploadMatchArtifact(ctx echo.Context) error {
	token := ctx.Request().Header.Get("Authorization")
	if strings.HasPrefix(token, "Bearer ") {
		token = strings.TrimPrefix(token, "Bearer ")
	}
	if token == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing match auth token")
	}

	match, err := models.GetMatchByTokenID(token)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid match auth token")
	}
	if match.Status != "started" {
		return echo.NewHTTPError(http.StatusForbidden, "match is not underway")
	}

	name := ctx.QueryParam("name")
	if !artifactNamePattern.MatchString(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid artifact name: must match [a-zA-Z0-9._-]{1,64}")
	}

	// Read up to MaxArtifactBytes+1 to detect oversize without trusting
	// Content-Length (which the client controls).
	body, err := io.ReadAll(io.LimitReader(ctx.Request().Body, MaxArtifactBytes+1))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "read body: "+err.Error())
	}
	if len(body) > MaxArtifactBytes {
		return echo.NewHTTPError(http.StatusRequestEntityTooLarge, fmt.Sprintf("artifact exceeds %d bytes", MaxArtifactBytes))
	}

	// Enforce the per-match count cap by checking the existing index.
	// Overwriting an existing name is fine; only reject when the new
	// name would push the count past the cap.
	reqCtx := ctx.Request().Context()
	index, err := server.S.AWS.GetMatchArtifactIndex(reqCtx, match.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "read artifact index: "+err.Error())
	}
	if _, exists := index[name]; !exists && len(index) >= MaxArtifactsPerMatch {
		return echo.NewHTTPError(http.StatusBadRequest,
			fmt.Sprintf("match already has %d artifacts (cap %d)", len(index), MaxArtifactsPerMatch))
	}

	contentType := ctx.Request().Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := server.S.AWS.PutMatchArtifact(reqCtx, match.ID, name, contentType, body); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "store artifact: "+err.Error())
	}
	return ctx.JSON(http.StatusOK, echo.Map{
		"name":         name,
		"size_bytes":   len(body),
		"content_type": contentType,
	})
}

// userArtifactsMatch is the per-match shape inside /user/artifacts.
// Includes minimal match metadata (so the client doesn't need to follow
// up with /results/<id>) plus the full artifact map.
type userArtifactsMatch struct {
	MatchID   string                   `json:"match_id"`
	GameID    string                   `json:"game_id"`
	EndedAt   string                   `json:"ended_at"`
	Artifacts map[string]artifactEntry `json:"artifacts"`
}

// ListUserArtifacts godoc
// @Summary      List the caller's match artifacts across games
// @Description  Returns the caller's recent match results that have at least one uploaded artifact. Optional `game_id` filters to a single game; optional repeated `name=` query params filter to results that have at least one of those artifact names. Each match in the response carries its full artifact map (with download URLs) regardless of which names matched the filter.
// @Tags         Matches
// @Produce      json
// @Security     BearerAuth
// @Param        game_id  query string false "Restrict to a single game"
// @Param        name     query string false "Repeatable: filter to matches having any of these artifact names"
// @Param        page     query int    false "Page number (default 0)"
// @Param        pageSize query int    false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "matches, next_page"
// @Failure      401 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /user/artifacts [get]
func ListUserArtifacts(ctx echo.Context) error {
	id, ok := ctx.Get("id").(string)
	if !ok || id == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}

	page, pageSize, err := parseUserArtifactsPagination(ctx)
	if err != nil {
		return err
	}

	var gameIDPtr *string
	if v := ctx.QueryParam("game_id"); v != "" {
		gameIDPtr = &v
	}

	// Echo's QueryParams() returns a url.Values; the same key can repeat.
	// e.g. ?name=replay&name=preview → []string{"replay","preview"}.
	filterNames := ctx.QueryParams()["name"]

	results, nextPage, err := models.GetMatchResultsWithArtifactsForPlayer(id, gameIDPtr, filterNames, page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	reqCtx := ctx.Request().Context()
	out := make([]userArtifactsMatch, 0, len(results))
	for _, mr := range results {
		index, err := server.S.AWS.GetMatchArtifactIndex(reqCtx, mr.ID)
		if err != nil {
			// Storage hiccup: log and skip rather than fail the whole
			// listing. The match is still in the model layer; the
			// caller can hit /matches/<id>/artifacts directly.
			continue
		}
		artifacts := make(map[string]artifactEntry, len(index))
		for name, meta := range index {
			artifacts[name] = artifactEntry{
				ContentType: meta.ContentType,
				SizeBytes:   meta.SizeBytes,
				UploadedAt:  meta.UploadedAt,
				URL:         fmt.Sprintf("/matches/%s/artifacts/%s", mr.ID, name),
			}
		}
		out = append(out, userArtifactsMatch{
			MatchID:   mr.ID,
			GameID:    mr.GameID,
			EndedAt:   mr.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Artifacts: artifacts,
		})
	}

	return ctx.JSON(http.StatusOK, echo.Map{
		"matches":   out,
		"next_page": nextPage,
	})
}

func parseUserArtifactsPagination(ctx echo.Context) (int, int, error) {
	page := 0
	pageSize := 10
	if v := ctx.QueryParam("page"); v != "" {
		fmt.Sscanf(v, "%d", &page)
	}
	if v := ctx.QueryParam("pageSize"); v != "" {
		fmt.Sscanf(v, "%d", &pageSize)
	}
	if page < 0 || pageSize < 1 || pageSize > 100 {
		return 0, 0, echo.NewHTTPError(http.StatusBadRequest, "invalid page/pageSize")
	}
	return page, pageSize, nil
}

// artifactEntry mirrors aws.MatchArtifactMeta plus a `url` pointer so a
// client can follow it to download. Defined locally so the API package
// doesn't leak the storage type into responses (and because we want
// `url` only on response shapes, not on the stored metadata).
type artifactEntry struct {
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	UploadedAt  string `json:"uploaded_at"`
	URL         string `json:"url"`
}

// resolveMatchArtifactsAuth applies the same auth gate as
// /results/<matchID>: the caller must either be a participant /
// game-owner / admin (CanUserSeeMatchResult) OR the game must have
// PublicResults=true. Returns the resolved MatchResult so callers
// don't re-fetch.
func resolveMatchArtifactsAuth(ctx echo.Context, matchID string) (*models.MatchResult, error) {
	mr, err := models.GetMatchResult(matchID)
	if err == gorm.ErrRecordNotFound {
		return nil, echo.NewHTTPError(http.StatusNotFound, "Match not found")
	} else if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	id, _ := ctx.Get("id").(string)
	if id == "" {
		return nil, echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}

	if mr.Game.PublicResults {
		return mr, nil
	}
	canSee, err := models.CanUserSeeMatchResult(id, matchID)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if !canSee {
		// Don't differentiate "not yours" from "doesn't exist" — same
		// rationale as the logs route.
		return nil, echo.NewHTTPError(http.StatusNotFound, "Match not found")
	}
	return mr, nil
}

// ListMatchArtifacts godoc
// @Summary      List artifacts attached to a match
// @Description  Returns every artifact uploaded by the game server during this match, with content_type, size_bytes, uploaded_at, and a download url. Auth gate matches the result-visibility rule for the game (PublicResults true → any auth; otherwise participant/owner/admin only).
// @Tags         Matches
// @Produce      json
// @Security     BearerAuth
// @Param        matchID path string true "Match UUID"
// @Success      200 {object} map[string]interface{} "artifacts"
// @Failure      401 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /matches/{matchID}/artifacts [get]
func ListMatchArtifacts(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	if _, err := resolveMatchArtifactsAuth(ctx, matchID); err != nil {
		return err
	}

	index, err := server.S.AWS.GetMatchArtifactIndex(ctx.Request().Context(), matchID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	out := make(map[string]artifactEntry, len(index))
	for name, meta := range index {
		out[name] = artifactEntry{
			ContentType: meta.ContentType,
			SizeBytes:   meta.SizeBytes,
			UploadedAt:  meta.UploadedAt,
			URL:         fmt.Sprintf("/matches/%s/artifacts/%s", matchID, name),
		}
	}
	return ctx.JSON(http.StatusOK, echo.Map{"artifacts": out})
}

// DownloadMatchArtifact godoc
// @Summary      Download one artifact's bytes
// @Description  Streams the raw bytes of one named artifact, with the Content-Type the game server uploaded it with. Same auth gate as ListMatchArtifacts.
// @Tags         Matches
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        matchID path string true "Match UUID"
// @Param        name    path string true "Artifact name"
// @Success      200 {string} string "raw artifact bytes"
// @Failure      401 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /matches/{matchID}/artifacts/{name} [get]
func DownloadMatchArtifact(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	if _, err := resolveMatchArtifactsAuth(ctx, matchID); err != nil {
		return err
	}

	name := ctx.Param("name")
	if !artifactNamePattern.MatchString(name) {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid artifact name")
	}

	body, contentType, err := server.S.AWS.GetMatchArtifact(ctx.Request().Context(), matchID, name)
	if err != nil {
		if errors.Is(err, aws.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "artifact not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.Response().Header().Set("Content-Type", contentType)
	ctx.Response().WriteHeader(http.StatusOK)
	_, _ = ctx.Response().Write(body)
	return nil
}

