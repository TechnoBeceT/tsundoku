package push_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entpush "github.com/technobecet/tsundoku/internal/ent/pushsubscription"
	"github.com/technobecet/tsundoku/internal/notify"
	"github.com/technobecet/tsundoku/internal/push"
)

// genSubKeys generates a valid P-256 subscription public key + 16-byte auth
// secret, base64url-encoded — exactly the shape a browser's PushManager hands
// out, so the webpush encryption step succeeds and the request reaches the test
// server (garbage keys would fail before any HTTP call).
func genSubKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen ecdh key: %v", err)
	}
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatalf("gen auth: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(authBytes)
}

// seedSubscription creates a subscription row pointing at endpoint with valid keys.
func seedSubscription(ctx context.Context, t *testing.T, client *ent.Client, endpoint string) {
	t.Helper()
	p256dh, auth := genSubKeys(t)
	client.PushSubscription.Create().
		SetEndpoint(endpoint).SetP256dh(p256dh).SetAuth(auth).SaveX(ctx)
}

// samplePayload is a minimal notification the sender marshals + delivers.
func samplePayload() notify.NewChapterNotification {
	return notify.NewChapterNotification{Total: 1, Title: "New chapter", Body: "1 new chapter"}
}

// TestSender_Prunes410 proves a 410 Gone endpoint's row is deleted while a 201
// endpoint's row survives and gets last_success_at stamped.
func TestSender_Prunes410(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := testdb.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			w.WriteHeader(http.StatusGone)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	seedSubscription(ctx, t, client, srv.URL+"/gone")
	seedSubscription(ctx, t, client, srv.URL+"/ok")

	pub, priv, err := push.EnsureVAPID(ctx, client)
	if err != nil {
		t.Fatalf("EnsureVAPID: %v", err)
	}
	push.NewSender(client, pub, priv, "mailto:owner@example.com").Push(ctx, samplePayload())

	// The gone endpoint's row is pruned.
	if n := client.PushSubscription.Query().Where(entpush.EndpointEQ(srv.URL + "/gone")).CountX(ctx); n != 0 {
		t.Fatalf("410 subscription not pruned: %d rows", n)
	}
	// The ok endpoint survives with last_success_at set.
	ok := client.PushSubscription.Query().Where(entpush.EndpointEQ(srv.URL + "/ok")).OnlyX(ctx)
	if ok.LastSuccessAt == nil {
		t.Fatalf("201 subscription missing last_success_at")
	}
	if ok.FailureCount != 0 {
		t.Fatalf("201 subscription failure_count = %d, want 0", ok.FailureCount)
	}
}

// TestSender_TransientBumpsFailureCount proves a 500 endpoint keeps its row but
// increments failure_count (not pruned below the threshold).
func TestSender_TransientBumpsFailureCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := testdb.New(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	seedSubscription(ctx, t, client, srv.URL+"/flaky")

	pub, priv, err := push.EnsureVAPID(ctx, client)
	if err != nil {
		t.Fatalf("EnsureVAPID: %v", err)
	}
	push.NewSender(client, pub, priv, "mailto:owner@example.com").Push(ctx, samplePayload())

	row := client.PushSubscription.Query().Where(entpush.EndpointEQ(srv.URL + "/flaky")).OnlyX(ctx)
	if row.FailureCount != 1 {
		t.Fatalf("failure_count = %d, want 1", row.FailureCount)
	}
}
