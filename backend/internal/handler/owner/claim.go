package owner

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"
	entpkg "github.com/technobecet/tsundoku/internal/ent"
	entowner "github.com/technobecet/tsundoku/internal/ent/owner"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

// Handler holds the dependencies for the owner HTTP handlers.
type Handler struct {
	client *entpkg.Client
	auth   *auth.Service
}

// NewHandler constructs a Handler with the given Ent client and auth service.
func NewHandler(client *entpkg.Client, authSvc *auth.Service) *Handler {
	return &Handler{client: client, auth: authSvc}
}

// Claim handles POST /api/owner/claim.
// If an owner already exists it returns 409 Conflict (fail-closed forever).
// Otherwise it creates the owner with a bcrypt-hashed password and returns
// a signed Bearer token. The zero-owners check and insert run inside a
// transaction so concurrent requests cannot both succeed.
func (h *Handler) Claim(c echo.Context) error {
	var req ClaimRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := req.validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	tok, err := h.claimTx(c.Request().Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, errOwnerExists) || isSerializationFailure(err) {
			return echo.NewHTTPError(http.StatusConflict, "owner already exists")
		}
		return err
	}

	return c.JSON(http.StatusOK, TokenResponse{Token: tok})
}

// claimTx performs the atomic first-owner creation inside a SERIALIZABLE
// transaction. It returns the signed token on success or errOwnerExists if
// an owner already exists.
func (h *Handler) claimTx(ctx context.Context, username, password string) (string, error) {
	var token string
	err := withSerializableTx(ctx, h.client, func(tx *entpkg.Tx) error {
		count, err := tx.Owner.Query().Count(ctx)
		if err != nil {
			return err
		}
		if count > 0 {
			return errOwnerExists
		}

		hash, err := h.auth.HashPassword(password)
		if err != nil {
			return err
		}

		o, err := tx.Owner.Create().
			SetUsername(username).
			SetPasswordHash(hash).
			Save(ctx)
		if err != nil {
			if entpkg.IsConstraintError(err) {
				return errOwnerExists
			}
			return err
		}

		tok, err := h.auth.Issue(o.ID)
		if err != nil {
			return err
		}
		token = tok
		return nil
	})
	return token, err
}

// Login handles POST /api/owner/login.
// It verifies the username and password; on success it returns a signed token.
// Wrong credentials return 401 without revealing which field was wrong.
func (h *Handler) Login(c echo.Context) error {
	var req LoginRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	ctx := c.Request().Context()

	o, err := h.client.Owner.Query().
		Where(entowner.UsernameEQ(req.Username)).
		Only(ctx)
	if err != nil {
		// Do not reveal whether username or password was wrong.
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}

	if !h.auth.VerifyPassword(o.PasswordHash, req.Password) {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid credentials")
	}

	tok, err := h.auth.Issue(o.ID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, TokenResponse{Token: tok})
}

// errOwnerExists is a sentinel error returned from within a transaction to
// signal that the claim must be rejected with 409 Conflict.
var errOwnerExists = errors.New("owner already exists")

// isSerializationFailure reports whether err is a PostgreSQL serialization
// failure (SQLSTATE 40001). Under SERIALIZABLE isolation, concurrent
// transactions that conflict will receive this error on commit; treating it
// as 409 Conflict is correct for the first-claim endpoint.
func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

// withSerializableTx runs fn inside a SERIALIZABLE Ent transaction,
// committing on success and rolling back on error.
// SERIALIZABLE isolation prevents phantom reads so concurrent claim requests
// cannot both observe zero owners and both succeed.
func withSerializableTx(ctx context.Context, client *entpkg.Client, fn func(*entpkg.Tx) error) error {
	tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
