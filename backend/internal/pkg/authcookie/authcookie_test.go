package authcookie_test

import (
	"net/http"
	"testing"

	"github.com/technobecet/tsundoku/internal/pkg/authcookie"
)

func TestNew_Attributes(t *testing.T) {
	c := authcookie.New("tok", true)
	if c.Name != authcookie.Name || c.Value != "tok" {
		t.Fatalf("name/value: %q=%q", c.Name, c.Value)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("flags wrong: %+v", c)
	}
	if c.MaxAge <= 0 {
		t.Fatalf("MaxAge should be positive, got %d", c.MaxAge)
	}
}

func TestClear_Expires(t *testing.T) {
	c := authcookie.Clear(false)
	if c.Value != "" || c.MaxAge != -1 || c.Secure {
		t.Fatalf("clear cookie wrong: %+v", c)
	}
}
