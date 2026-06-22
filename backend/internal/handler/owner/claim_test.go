package owner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	entpkg "github.com/technobecet/tsundoku/internal/ent"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/handler/owner"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

func newHandlerWithClient(t *testing.T) (*owner.Handler, *echo.Echo, *entpkg.Client) {
	t.Helper()
	client := testdb.New(t)
	svc := auth.NewService("handler-test-secret")
	h := owner.NewHandler(client, svc)
	e := echo.New()
	return h, e, client
}

func callClaim(t *testing.T, e *echo.Echo, h *owner.Handler, username, password string) int {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/api/owner/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := h.Claim(c); err != nil {
		he, ok := err.(*echo.HTTPError)
		if ok {
			return he.Code
		}
		return http.StatusInternalServerError
	}
	return rec.Code
}

func callLogin(t *testing.T, e *echo.Echo, h *owner.Handler, username, password string) int {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	req := httptest.NewRequest(http.MethodPost, "/api/owner/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := h.Login(c); err != nil {
		he, ok := err.(*echo.HTTPError)
		if ok {
			return he.Code
		}
		return http.StatusInternalServerError
	}
	return rec.Code
}

func ownerCount(t *testing.T, client *entpkg.Client) int {
	t.Helper()
	count, err := client.Owner.Query().Count(context.Background())
	if err != nil {
		t.Fatalf("Owner count: %v", err)
	}
	return count
}

func TestClaim_First(t *testing.T) {
	h, e, client := newHandlerWithClient(t)

	body := `{"username":"admin","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/owner/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := h.Claim(c); err != nil {
		t.Fatalf("Claim: unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("Claim: expected 200, got %d", rec.Code)
	}

	var resp owner.TokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Claim: decode response: %v", err)
	}
	if resp.Token == "" {
		t.Error("Claim: expected non-empty token in response")
	}

	if count := ownerCount(t, client); count != 1 {
		t.Errorf("expected 1 owner row, got %d", count)
	}
}

func TestClaim_Second_Conflict(t *testing.T) {
	h, e, client := newHandlerWithClient(t)

	code1 := callClaim(t, e, h, "admin", "password123")
	if code1 != http.StatusOK {
		t.Fatalf("first claim: expected 200, got %d", code1)
	}

	code2 := callClaim(t, e, h, "admin2", "password456")
	if code2 != http.StatusConflict {
		t.Errorf("second claim: expected 409, got %d", code2)
	}

	if count := ownerCount(t, client); count != 1 {
		t.Errorf("expected 1 owner row after second claim, got %d", count)
	}
}

func TestClaim_Concurrent(t *testing.T) {
	h, e, client := newHandlerWithClient(t)

	const n = 20
	codes := make([]int, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			codes[i] = callClaim(t, e, h, fmt.Sprintf("user%d", i), "password123")
		}(i)
	}
	wg.Wait()

	okCount := 0
	conflictCount := 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		default:
			t.Errorf("unexpected status code: %d", code)
		}
	}

	if okCount != 1 {
		t.Errorf("concurrent claim: expected exactly 1 success, got %d", okCount)
	}
	if conflictCount != n-1 {
		t.Errorf("concurrent claim: expected %d conflicts, got %d", n-1, conflictCount)
	}

	if count := ownerCount(t, client); count != 1 {
		t.Errorf("expected exactly 1 owner row after concurrent claims, got %d", count)
	}
}

func TestLogin_Success(t *testing.T) {
	h, e, _ := newHandlerWithClient(t)

	code := callClaim(t, e, h, "admin", "mypassword")
	if code != http.StatusOK {
		t.Fatalf("claim: expected 200, got %d", code)
	}

	loginCode := callLogin(t, e, h, "admin", "mypassword")
	if loginCode != http.StatusOK {
		t.Errorf("login: expected 200, got %d", loginCode)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	h, e, _ := newHandlerWithClient(t)

	code := callClaim(t, e, h, "admin", "correctpassword")
	if code != http.StatusOK {
		t.Fatalf("claim: expected 200, got %d", code)
	}

	loginCode := callLogin(t, e, h, "admin", "wrongpassword")
	if loginCode != http.StatusUnauthorized {
		t.Errorf("login wrong password: expected 401, got %d", loginCode)
	}
}

func TestLogin_WrongUsername(t *testing.T) {
	h, e, _ := newHandlerWithClient(t)

	code := callClaim(t, e, h, "admin", "correctpassword")
	if code != http.StatusOK {
		t.Fatalf("claim: expected 200, got %d", code)
	}

	loginCode := callLogin(t, e, h, "notadmin", "correctpassword")
	if loginCode != http.StatusUnauthorized {
		t.Errorf("login wrong username: expected 401, got %d", loginCode)
	}
}
