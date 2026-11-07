package client

// GetBlob checks what it delivered against the blob's address: a corrupt or truncated
// body ends in a *BlobIntegrityError, never a clean EOF. An empty 200 where a result is
// expected is an error. FAIL-SAFE: httptest on 127.0.0.1 only.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sum(s string) string {
	b := sha256.Sum256([]byte(s))
	return hex.EncodeToString(b[:])
}

func blobServer(t *testing.T, status int, body string) *Client {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return newTestClient(t, ts.URL)
}

func TestGetBlobDetectsCorruptBody(t *testing.T) {
	good := strings.Repeat("shared bytes ", 40000) // several reads
	for name, body := range map[string]string{"corrupted": "X" + good[1:], "truncated": good[:len(good)/2], "empty": ""} {
		t.Run(name, func(t *testing.T) {
			rc, err := blobServer(t, 200, body).GetBlob(sum(good))
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			got, err := io.ReadAll(rc)
			var bad *BlobIntegrityError
			if !errors.As(err, &bad) || !IsBlobIntegrityError(err) {
				t.Fatalf("want a *BlobIntegrityError at the end of the stream, got %v", err)
			}
			if bad.Hash != sum(good) || bad.Got != sum(body) || bad.Size != int64(len(body)) || string(got) != body {
				t.Fatalf("the error must say what arrived: %+v (%d bytes)", bad, len(got))
			}
			if _, again := rc.Read(make([]byte, 8)); !IsBlobIntegrityError(again) {
				t.Fatalf("the verdict is sticky, never a later clean EOF: %v", again)
			}
		})
	}
}

func TestGetBlobPassesACorrectBody(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "small": "x", "large": strings.Repeat("0123456789", 300000)} {
		for _, hash := range []string{sum(body), strings.ToUpper(sum(body))} {
			rc, err := blobServer(t, 200, body).GetBlob(hash)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(rc)
			rc.Close()
			if err != nil || string(got) != body {
				t.Fatalf("%s: %v (%d bytes)", name, err, len(got))
			}
		}
	}
	// Closing early checks nothing and is no error.
	rc, err := blobServer(t, 200, "abcdef").GetBlob(sum("other"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Read(make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	// A 404 stays the APIError it was.
	_, err = blobServer(t, 404, `{"error":"no such blob"}`).GetBlob(sum("x"))
	var api *APIError
	if !errors.As(err, &api) || api.Status != 404 || IsBlobIntegrityError(err) {
		t.Fatalf("a 404 is an APIError: %v", err)
	}
}

func TestEmpty200IsAnError(t *testing.T) {
	if _, err := blobServer(t, 200, "").Whoami(); err == nil || !strings.Contains(err.Error(), "empty body") {
		t.Fatalf("an empty 200 where a result is expected must be an error: %v", err)
	}
	if err := blobServer(t, 204, "").do("POST", "/v1/x", map[string]any{"a": 1}, &map[string]any{}); err != nil {
		t.Fatalf("204 means no body: %v", err)
	}
	if err := blobServer(t, 200, "").do("POST", "/v1/x", map[string]any{"a": 1}, nil); err != nil {
		t.Fatalf("a caller expecting no result takes an empty 200: %v", err)
	}
}
