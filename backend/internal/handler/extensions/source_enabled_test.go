package extensions_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	handler "github.com/technobecet/tsundoku/internal/handler/extensions"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// TestSetSourceEnabled_RoundTrip proves the toggle applies the write then
// RE-READS via Sources for the authoritative post-write state (§16) — the
// fake's SetSourceEnabled mutates the underlying source, and the response
// must reflect that mutation, not the request echo.
func TestSetSourceEnabled_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"disable", `{"enabled":false}`, false},
		{"reEnable", `{"enabled":true}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeClient{sources: []suwayomicli.Source{{ID: "src-ru", Name: "Comick RU", Lang: "ru"}}}
			env := newTestEnv(t, fc)

			rec := env.do(http.MethodPatch, "/api/suwayomi/sources/src-ru/enabled", tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("SetSourceEnabled: want 200, got %d (%s)", rec.Code, rec.Body.String())
			}
			if !fc.setEnabledCalled || fc.lastEnabledSourceID != "src-ru" || fc.lastEnabledValue != tc.want {
				t.Errorf("write not dispatched correctly: called=%v src=%q val=%v", fc.setEnabledCalled, fc.lastEnabledSourceID, fc.lastEnabledValue)
			}
			var got handler.SourceEnabledDTO
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.SourceID != "src-ru" || got.Enabled != tc.want {
				t.Errorf("response = %+v, want {src-ru %v} (must be the re-read, not the request echo)", got, tc.want)
			}
		})
	}
}

// TestSetSourceEnabled_MissingEnabled400 proves a missing `enabled` key is a
// 400 (not silently defaulting to false/disable), and no write is dispatched.
func TestSetSourceEnabled_MissingEnabled400(t *testing.T) {
	fc := &fakeClient{sources: []suwayomicli.Source{{ID: "src-ru", Name: "Comick RU", Lang: "ru"}}}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/sources/src-ru/enabled", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.setEnabledCalled {
		t.Error("a write was dispatched despite a missing enabled field")
	}
}

// TestSetSourceEnabled_InvalidJSON400 proves a malformed body is a 400.
func TestSetSourceEnabled_InvalidJSON400(t *testing.T) {
	env := newTestEnv(t, &fakeClient{})
	rec := env.do(http.MethodPatch, "/api/suwayomi/sources/src-ru/enabled", `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid json: want 400, got %d", rec.Code)
	}
}

// TestSetSourceEnabled_BlankSourceID400 proves a whitespace-only :sourceId (a
// "%20" path segment) is rejected before any client call.
func TestSetSourceEnabled_BlankSourceID400(t *testing.T) {
	fc := &fakeClient{}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/sources/%20/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank sourceId: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if fc.setEnabledCalled {
		t.Error("validation failure must not call SetSourceEnabled")
	}
}

// TestSetSourceEnabled_Upstream502 proves a Suwayomi write failure is a 502.
func TestSetSourceEnabled_Upstream502(t *testing.T) {
	fc := &fakeClient{setEnabledErr: errors.New("graphql rejected")}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/sources/src-ru/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream write failure: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceEnabled_ReadBack502 proves a failure of the post-write re-read
// (Sources) is also a 502, mirroring the extension-management endpoints.
func TestSetSourceEnabled_ReadBack502(t *testing.T) {
	fc := &fakeClient{sourcesErr: errors.New("connection reset")}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/sources/src-ru/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("read-back fail: want 502, got %d", rec.Code)
	}
	if !fc.setEnabledCalled {
		t.Error("the write should have been attempted before the read-back")
	}
}

// TestSetSourceEnabled_NotFoundAfterReread proves a sourceId absent from the
// post-write Sources() re-read (the source vanished between the write and the
// read) is a 404, not a false 200 with a zero-value DTO.
func TestSetSourceEnabled_NotFoundAfterReread(t *testing.T) {
	fc := &fakeClient{sources: []suwayomicli.Source{{ID: "some-other-source", Name: "Other", Lang: "en"}}}
	env := newTestEnv(t, fc)
	rec := env.do(http.MethodPatch, "/api/suwayomi/sources/src-ru/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("vanished source: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestSetSourceEnabled_Unauthorized proves the route is behind RequireOwner.
func TestSetSourceEnabled_Unauthorized(t *testing.T) {
	fc := &fakeClient{sources: []suwayomicli.Source{{ID: "src-ru", Name: "Comick RU", Lang: "ru"}}}
	env := newTestEnv(t, fc)
	rec := env.noAuth(http.MethodPatch, "/api/suwayomi/sources/src-ru/enabled", `{"enabled":false}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("SetSourceEnabled no token: want 401, got %d", rec.Code)
	}
	if fc.setEnabledCalled {
		t.Error("SetSourceEnabled must not be called on an unauthorized request")
	}
}
