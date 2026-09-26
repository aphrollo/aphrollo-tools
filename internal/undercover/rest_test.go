package undercover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRESTClient_SendsTheTokenAndReadsAndPatches(t *testing.T) {
	t.Parallel()
	var patched map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		if wantJSON := r.Method == http.MethodPatch; (r.Header.Get("Content-Type") == "application/json") != wantJSON {
			http.Error(w, "content type", http.StatusBadRequest)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/7":
			io.WriteString(w, `{"title":"t"}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/o/r/pulls/7":
			json.NewDecoder(r.Body).Decode(&patched)
			io.WriteString(w, `{}`)
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := RESTClient{BaseURL: srv.URL, Token: "tok", HTTP: &http.Client{Timeout: 10 * time.Second}}
	ctx := context.Background()

	got, err := c.Get(ctx, "/repos/o/r/pulls/7")
	if err != nil || string(got) != `{"title":"t"}` {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := c.Patch(ctx, "/repos/o/r/pulls/7", map[string]string{"body": "kept"}); err != nil {
		t.Fatal(err)
	}
	if patched["body"] != "kept" {
		t.Errorf("patched = %v", patched)
	}
	if _, err := c.Get(ctx, "/repos/o/r/missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404 must be an error naming the status, got %v", err)
	}
	err = c.Patch(ctx, "/repos/o/r/missing", nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusNotFound {
		t.Errorf("a failed PATCH must be an *HTTPError carrying the status, got %v", err)
	}
}
