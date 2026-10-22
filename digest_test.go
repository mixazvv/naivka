package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyAsset(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "core.zip")
	if err := os.WriteFile(file, []byte("sing-box"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("sing-box"))
	good := hex.EncodeToString(h[:])

	digest := "sha256:" + good
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/SagerNet/sing-box/releases/tags/v1.14.3" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"assets":[{"name":"other.zip","digest":"sha256:00"},{"name":"core.zip","digest":"` + digest + `"}]}`))
	}))
	defer srv.Close()
	old, oldRetry := githubAPI, digestRetry
	githubAPI, digestRetry = srv.URL+"/", time.Millisecond
	defer func() { githubAPI, digestRetry = old, oldRetry }()

	if err := verifyAsset("SagerNet/sing-box", "v1.14.3", "core.zip", file); err != nil {
		t.Fatalf("верный файл: %v", err)
	}
	digest = "sha256:" + hex.EncodeToString(make([]byte, 32))
	if err := verifyAsset("SagerNet/sing-box", "v1.14.3", "core.zip", file); err == nil {
		t.Error("подменённый файл принят")
	}
	digest = "" // старый релиз: GitHub хеша не знает — непроверенный файл не ставится
	if err := verifyAsset("SagerNet/sing-box", "v1.14.3", "core.zip", file); err == nil {
		t.Error("файл без хеша принят")
	}
	status = 403 // лимит запросов к API
	if err := verifyAsset("SagerNet/sing-box", "v1.14.3", "core.zip", file); err == nil {
		t.Error("файл принят без доступа к API")
	}
}
