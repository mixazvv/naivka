package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestWallpaperUploadServeDelete(t *testing.T) {
	a := &App{dir: t.TempDir(), token: "tok", settings: DefaultSettings()}
	req := func(method string, body []byte, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/wallpaper", bytes.NewReader(body))
		r.Header.Set("X-Naivka-Token", token)
		w := httptest.NewRecorder()
		a.wallHandler(w, r)
		return w
	}

	var pic bytes.Buffer
	_ = png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 4, 4)))

	if w := req("POST", pic.Bytes(), "wrong"); w.Code != http.StatusForbidden {
		t.Fatalf("без токена: код %d", w.Code)
	}
	// Не картинка и не видео — отказ, даже если это «просто текст».
	if w := req("POST", []byte("#!/bin/sh\necho hi\n"), "tok"); w.Code != http.StatusBadRequest {
		t.Fatalf("текстовый файл принят: код %d", w.Code)
	}

	w := req("POST", pic.Bytes(), "tok")
	if w.Code != 200 {
		t.Fatalf("загрузка PNG: код %d, %s", w.Code, w.Body)
	}
	var ws WallSettings
	_ = json.Unmarshal(w.Body.Bytes(), &ws)
	if ws.Kind != "image" || ws.Ext != ".png" || ws.Ver == 0 {
		t.Fatalf("сведения о фоне: %+v", ws)
	}

	w = req("GET", nil, "tok")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), pic.Bytes()) {
		t.Fatalf("отдача фона: код %d, тип %q", w.Code, w.Header().Get("Content-Type"))
	}

	if w := req("DELETE", nil, "tok"); w.Code != 200 {
		t.Fatalf("удаление: код %d", w.Code)
	}
	if _, err := os.Stat(a.wallPath(".png")); !os.IsNotExist(err) {
		t.Fatal("файл фона остался после удаления")
	}
	if w := req("GET", nil, "tok"); w.Code != http.StatusNotFound {
		t.Fatalf("после удаления: код %d", w.Code)
	}
}

func TestWallSettingsNormalize(t *testing.T) {
	w := WallSettings{Kind: "image", Ext: ".exe", Dim: 300, Blur: -5}
	w.normalize()
	if w.Kind != "" || w.Ext != "" || w.Dim != 90 || w.Blur != 0 {
		t.Fatalf("normalize: %+v", w)
	}
}
