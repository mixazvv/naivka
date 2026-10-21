package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Свой фон окна: фото или короткое видео, которое человек выбрал сам. Файл лежит в папке данных
// Naivka (wallpaper.<расширение>) и никуда не отправляется; окно получает его тем же локальным
// адресом, что и остальной интерфейс, — с токеном в заголовке.

// WallSettings — фон окна. Kind и Ext выставляет только загрузка (по содержимому файла).
type WallSettings struct {
	Kind  string `json:"kind"`  // "" — фона нет; image | video
	Ext   string `json:"ext"`   // расширение сохранённого файла
	Dim   int    `json:"dim"`   // затемнение, 0–90 %
	Blur  int    `json:"blur"`  // размытие, 0–30 px
	React bool   `json:"react"` // подсвечивать по состоянию подключения
	Pixel bool   `json:"pixel"` // пиксельный фон (в теме «Пиксели» — всегда)
	Fit   string `json:"fit"`   // cover — заполнить (с обрезкой) | contain — целиком | fill — растянуть
	Scale int    `json:"scale"` // масштаб, 50–200 %
	Ver   int64  `json:"ver"`   // меняется при каждой загрузке — окно знает, что файл новый
}

const wallMaxSize = 80 << 20

// Допустимые форматы: определяются по первым байтам файла, а не по имени.
var wallTypes = map[string]struct{ kind, ext string }{
	"image/jpeg": {"image", ".jpg"},
	"image/png":  {"image", ".png"},
	"image/gif":  {"image", ".gif"},
	"image/webp": {"image", ".webp"},
	"video/mp4":  {"video", ".mp4"},
	"video/webm": {"video", ".webm"},
}

func (w *WallSettings) normalize() {
	w.Dim = clampInt(w.Dim, 0, 90)
	w.Blur = clampInt(w.Blur, 0, 30)
	if w.Fit != "contain" && w.Fit != "fill" {
		w.Fit = "cover"
	}
	if w.Scale == 0 {
		w.Scale = 100 // настройки прежних версий
	}
	w.Scale = clampInt(w.Scale, 50, 200)
	ok := false
	for _, t := range wallTypes {
		if t.kind == w.Kind && t.ext == w.Ext {
			ok = true
		}
	}
	if !ok {
		w.Kind, w.Ext = "", ""
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (a *App) wallPath(ext string) string { return filepath.Join(a.dir, "wallpaper"+ext) }

// removeWallFiles убирает все сохранённые фоны, кроме keep.
func (a *App) removeWallFiles(keep string) {
	for _, t := range wallTypes {
		if p := a.wallPath(t.ext); p != keep {
			_ = os.Remove(p)
		}
	}
}

// wallHandler: GET — файл фона, POST — загрузить новый (тело — сам файл), DELETE — убрать фон.
func (a *App) wallHandler(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Naivka-Token")), []byte(a.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch r.Method {
	case "GET", "HEAD":
		ws := a.settingsCopy().Wall
		if ws.Kind == "" {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(a.wallPath(ws.Ext))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		for ct, t := range wallTypes {
			if t.ext == ws.Ext {
				w.Header().Set("Content-Type", ct)
			}
		}
		http.ServeContent(w, r, "", time.Time{}, f)
	case "POST":
		ws, err := a.saveWall(r)
		writeJSON(w, ws, err)
	case "DELETE":
		a.mu.Lock()
		a.settings.Wall.Kind, a.settings.Wall.Ext = "", ""
		ns := a.settings.clone()
		a.mu.Unlock()
		a.removeWallFiles("")
		writeJSON(w, ns.Wall, SaveSettings(a.dir, ns))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) saveWall(r *http.Request) (WallSettings, error) {
	body := http.MaxBytesReader(nil, r.Body, wallMaxSize+1)
	tmp := filepath.Join(a.dir, "wallpaper.part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return WallSettings{}, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(body, head)
	head = head[:n]
	t, ok := wallTypes[http.DetectContentType(head)]
	if !ok {
		f.Close()
		os.Remove(tmp)
		return WallSettings{}, errors.New("Подойдёт фото (JPG, PNG, WEBP, GIF) или видео (MP4, WEBM)")
	}
	_, err = f.Write(head)
	if err == nil {
		_, err = io.Copy(f, body)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			return WallSettings{}, fmt.Errorf("Файл больше %d МБ — возьмите поменьше", wallMaxSize>>20)
		}
		return WallSettings{}, err
	}
	dst := a.wallPath(t.ext)
	_ = os.Remove(dst)
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return WallSettings{}, err
	}
	a.removeWallFiles(dst)
	a.mu.Lock()
	a.settings.Wall.Kind, a.settings.Wall.Ext, a.settings.Wall.Ver = t.kind, t.ext, time.Now().UnixMilli()
	ns := a.settings.clone()
	a.mu.Unlock()
	return ns.Wall, SaveSettings(a.dir, ns)
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
