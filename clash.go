package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Clash struct {
	base   string
	secret string
	hc     *http.Client
}

// clashTransport — один на всю программу: раньше каждый вызов Core.Clash() создавал свой,
// и его открытое соединение с ядром так и оставалось висеть (несколько в секунду, пока подключены).
var clashTransport = &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}

func NewClash(port int, secret string) *Clash {
	return &Clash{
		base:   fmt.Sprintf("http://127.0.0.1:%d", port),
		secret: secret,
		// Без прокси из окружения: API всегда локальный.
		hc: &http.Client{Timeout: 12 * time.Second, Transport: clashTransport},
	}
}

func (c *Clash) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = resp.Status
		}
		return fmt.Errorf("%s", e.Message)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *Clash) Ping() error {
	var v struct {
		Version string `json:"version"`
	}
	return c.do("GET", "/version", nil, &v)
}

type ProxyView struct {
	Selected string         `json:"selected"` // что выбрано в селекторе (auto или s-<id>)
	AutoNow  string         `json:"autoNow"`  // какой сервер сейчас выбрал автовыбор
	WLNow    string         `json:"wlNow"`    // какой сервер подписки сейчас выбран
	Delays   map[string]int `json:"delays"`   // тег -> последняя задержка, 0 = нет ответа
}

func (c *Clash) Proxies() (*ProxyView, error) {
	var raw struct {
		Proxies map[string]struct {
			Type    string `json:"type"`
			Now     string `json:"now"`
			History []struct {
				Delay int `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := c.do("GET", "/proxies", nil, &raw); err != nil {
		return nil, err
	}
	v := &ProxyView{Delays: map[string]int{}}
	for name, p := range raw.Proxies {
		switch name {
		case tagProxy:
			v.Selected = p.Now
		case tagAuto:
			v.AutoNow = p.Now
		case "wl":
			v.WLNow = p.Now
		}
		if (strings.HasPrefix(name, "s-") || strings.HasPrefix(name, "w-")) && len(p.History) > 0 {
			v.Delays[name] = p.History[len(p.History)-1].Delay
		}
	}
	return v, nil
}

func (c *Clash) Select(tag string) error { return c.SelectIn(tagProxy, tag) }

// SelectIn переключает группу-переключатель group на tag.
func (c *Clash) SelectIn(group, tag string) error {
	return c.do("PUT", "/proxies/"+url.PathEscape(group), map[string]string{"name": tag}, nil)
}

func (c *Clash) Delay(tag, testURL string) (int, error) {
	var v struct {
		Delay int `json:"delay"`
	}
	q := url.Values{"timeout": {"5000"}, "url": {testURL}} // за 5 с живой сервер успевает, мёртвый не держит проверку
	err := c.do("GET", "/proxies/"+url.PathEscape(tag)+"/delay?"+q.Encode(), nil, &v)
	return v.Delay, err
}

// GroupDelay проверяет сразу все серверы группы (параллельно в ядре).
// Ответ — тег → задержка в мс; не ответившие серверы в ответ не попадают.
func (c *Clash) GroupDelay(group, testURL string, timeoutMs int) (map[string]int, error) {
	out := map[string]int{}
	q := url.Values{"timeout": {fmt.Sprint(timeoutMs)}, "url": {testURL}}
	err := c.do("GET", "/group/"+url.PathEscape(group)+"/delay?"+q.Encode(), nil, &out)
	return out, err
}

type Conn struct {
	ID       string    `json:"id"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
	Chains   []string  `json:"chains"`
	Rule     string    `json:"rule"`
	Payload  string    `json:"rulePayload"`
	Metadata struct {
		Network     string `json:"network"`
		Type        string `json:"type"`
		Host        string `json:"host"`
		DestIP      string `json:"destinationIP"`
		DestPort    string `json:"destinationPort"`
		SourcePort  string `json:"sourcePort"`
		ProcessPath string `json:"processPath"`
	} `json:"metadata"`
}

type ConnsView struct {
	DownloadTotal int64  `json:"downloadTotal"`
	UploadTotal   int64  `json:"uploadTotal"`
	Count         int    `json:"count"`
	Connections   []Conn `json:"connections"`
}

func (c *Clash) Connections(limit int) (*ConnsView, error) {
	var v ConnsView
	if err := c.do("GET", "/connections", nil, &v); err != nil {
		return nil, err
	}
	v.Count = len(v.Connections)
	sort.Slice(v.Connections, func(i, j int) bool { return v.Connections[i].Start.After(v.Connections[j].Start) })
	if len(v.Connections) > limit {
		v.Connections = v.Connections[:limit]
	}
	if v.Connections == nil {
		v.Connections = []Conn{}
	}
	return &v, nil
}

// ConnectionsRaw — все активные соединения без сортировки (для статистики).
func (c *Clash) ConnectionsRaw() (*ConnsView, error) {
	var v ConnsView
	if err := c.do("GET", "/connections", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *Clash) CloseConn(id string) error {
	return c.do("DELETE", "/connections/"+url.PathEscape(id), nil, nil)
}

func (c *Clash) CloseAll() error { return c.do("DELETE", "/connections", nil, nil) }
