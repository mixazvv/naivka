package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// githubAPI — адрес API GitHub; в тестах подменяется на локальный сервер.
var githubAPI = "https://api.github.com/"

// assetDigest — SHA-256 файла релиза, который GitHub посчитал сам при загрузке файла
// (поле digest в API). Пустая строка — GitHub хеша не знает: файл загружен до появления
// этой функции (лето 2025) или в релизе нет такого файла.
func assetDigest(repo, tag, name string) (string, error) {
	req, _ := http.NewRequest("GET", githubAPI+"repos/"+repo+"/releases/tags/"+tag, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Naivka")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API GitHub ответил %s", resp.Status)
	}
	var rel struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return "", err
	}
	for _, a := range rel.Assets {
		if a.Name == name {
			if d, ok := strings.CutPrefix(a.Digest, "sha256:"); ok {
				return strings.ToLower(d), nil
			}
			return "", nil
		}
	}
	return "", nil
}

// verifyAsset сверяет скачанный файл с хешем, который GitHub хранит для этого файла релиза.
// Хеш не совпал — ошибка, файл ставить нельзя. GitHub хеша не дал (старый релиз, API недоступно
// или исчерпан лимит запросов) — verified == false: файл получен только по HTTPS, без сверки.
func verifyAsset(repo, tag, name, path string) (verified bool, err error) {
	want, err := assetDigest(repo, tag, name)
	if err != nil || want == "" {
		return false, nil
	}
	got, err := fileSHA256(path)
	if err != nil {
		return false, err
	}
	if got != want {
		return false, fmt.Errorf("%s: контрольная сумма не совпала с той, что хранит GitHub, — файл повреждён или подменён, установка отменена", name)
	}
	return true, nil
}
