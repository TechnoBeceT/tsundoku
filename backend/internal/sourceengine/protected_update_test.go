package sourceengine_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestProtectedExtensionUpdateRoundTripAndStructuredConflict(t *testing.T) {
	var activated sourceengine.ActivatePreparedExtensionUpdate
	doer := doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing control auth")
		}
		status, body := http.StatusOK, ""
		switch r.URL.Path {
		case "/extensions/pkg.test/prepare-update":
			body = `{"token":"t","pkgName":"pkg.test","installedVersionCode":1,"candidateVersionCode":2,"installedSourceIds":[11,22],"candidateSourceIds":[22],"removedSourceIds":[11],"mutationSequence":7}`
		case "/extensions/pkg.test/activate-prepared-update":
			if err := json.NewDecoder(r.Body).Decode(&activated); err != nil {
				t.Fatal(err)
			}
			status, body = http.StatusConflict, `{"error":"protected","code":"source_retirement_conflict","pkgName":"pkg.test","sourceIds":[11]}`
		case "/extensions/pkg.test/prepared-update":
			body = `{"ok":true}`
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	c := sourceengine.New("http://engine", doer, "secret")
	u, err := sourceengine.ProtectedUpdaterFor(c)
	requireProtectedNoError(t, err)
	p, err := u.PrepareExtensionUpdate(context.Background(), "pkg.test")
	requireProtectedNoError(t, err)
	_, err = u.ActivatePreparedExtensionUpdate(context.Background(), sourceengine.ActivatePreparedExtensionUpdate{PreparedExtensionUpdate: p, ProtectedSourceIDs: []int64{11}})
	var conflict *sourceengine.SourceRetirementConflictError
	assertProtectedConflict(t, conflict, err)
	assertActivatedWitness(t, activated)
	requireProtectedNoError(t, u.DiscardPreparedExtensionUpdate(context.Background(), "pkg.test", "t"))
}

func requireProtectedNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func assertProtectedConflict(t *testing.T, conflict *sourceengine.SourceRetirementConflictError, err error) {
	t.Helper()
	if !errors.As(err, &conflict) || conflict.Code != "source_retirement_conflict" || len(conflict.SourceIDs) != 1 || conflict.SourceIDs[0] != 11 {
		t.Fatalf("conflict=%#v err=%v", conflict, err)
	}
}
func assertActivatedWitness(t *testing.T, activated sourceengine.ActivatePreparedExtensionUpdate) {
	t.Helper()
	if len(activated.ProtectedSourceIDs) != 1 || activated.Token != "t" {
		t.Fatalf("activated=%+v", activated)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
