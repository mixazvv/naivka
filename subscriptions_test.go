package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const subscriptionFixture = "trojan://secret@one.example:443#One\nnaive+https://u:pass@two.example:443#Two"

func TestSubscriptionFormatsAndDedup(t *testing.T) {
	for _, body := range []string{"\ufeff# comment\r\n" + subscriptionFixture, base64.StdEncoding.EncodeToString([]byte(subscriptionFixture)), base64.RawStdEncoding.EncodeToString([]byte(subscriptionFixture)), base64.URLEncoding.EncodeToString([]byte(subscriptionFixture))} {
		list, skipped, e := parseSubscription(body)
		if e != nil || len(list) != 2 || skipped != 0 {
			t.Fatal(len(list), skipped, e)
		}
	}
	list, _, e := parseSubscription(subscriptionFixture + "\ntrojan://secret@one.example:443#OtherName")
	if e != nil || len(list) != 2 {
		t.Fatal(list, e)
	}
	for _, body := range []string{"", "<html>login secret-token</html>", `{"outbounds":[]}`, "proxies: []"} {
		_, _, e := parseSubscription(body)
		if e == nil || strings.Contains(e.Error(), "secret-token") {
			t.Fatal(e)
		}
	}
	if _, skipped, e := parseSubscription(subscriptionFixture + "\nunsupported://private-key"); e != nil || skipped != 1 {
		t.Fatal(skipped, e)
	}
	for _, raw := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://user:pass@host/", "https:///", "http://"} {
		if _, e := subscriptionURL(raw); e == nil {
			t.Fatal(raw)
		}
	}
}

type subscriptionTransport func(*http.Request) (*http.Response, error)

func (f subscriptionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSubscriptionFetchBoundaries(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		broken bool
		ok     bool
	}{{200, subscriptionFixture, false, true}, {403, "secret-token", false, false}, {200, strings.Repeat("x", subscriptionLimit+1), false, false}, {200, "", true, false}, {200, "<html>private</html>", false, false}} {
		hc := &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
			if tc.broken {
				return nil, errors.New("https://private/secret-token")
			}
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header), Request: r}, nil
		})}
		list, _, e := fetchSubscription(context.Background(), hc, "https://private/secret-token")
		if (e == nil) != tc.ok {
			t.Fatal(e)
		}
		if tc.ok && len(list) != 2 {
			t.Fatal(list)
		}
		if e != nil && (strings.Contains(e.Error(), "secret-token") || strings.Contains(e.Error(), "private")) {
			t.Fatal("credential leak", e)
		}
	}
}
func TestSubscriptionReplacePreservesSelectionAndFavorites(t *testing.T) {
	s := DefaultSettings()
	list, _, _ := parseSubscription(subscriptionFixture)
	if e := replaceSubscription(s, "a", list, false); e != nil {
		t.Fatal(e)
	}
	id := s.Servers[0].ID
	s.Selected = id
	s.Servers[0].Fav = true
	rotated := append([]Server(nil), list...)
	rotated[0].Password = "rotated"
	rotated[0].Name = "New name"
	if e := replaceSubscription(s, "a", rotated, true); e != nil {
		t.Fatal(e)
	}
	if s.Selected != id || s.Servers[0].ID != id || !s.Servers[0].Fav || s.Servers[0].Password != "rotated" {
		t.Fatal(s.Servers)
	}
	before := s.clone()
	if e := replaceSubscription(s, "a", list[1:], true); e == nil {
		t.Fatal("removed active server")
	}
	if s.fingerprint() != before.fingerprint() {
		t.Fatal("failed update changed configuration")
	}
	if e := replaceSubscription(s, "a", list[1:], false); e != nil || s.Selected != "auto" {
		t.Fatal(e, s.Selected)
	}
	// Different credentials on the same endpoint must retain distinct stable IDs.
	list[1] = list[0]
	list[1].Password = "second"
	s = DefaultSettings()
	_ = replaceSubscription(s, "a", list, false)
	if s.Servers[0].ID == s.Servers[1].ID {
		t.Fatal("credential collision")
	}
	ids := []string{s.Servers[0].ID, s.Servers[1].ID}
	_ = replaceSubscription(s, "a", []Server{list[1], list[0]}, false)
	if s.Servers[0].ID != ids[1] || s.Servers[1].ID != ids[0] {
		t.Fatal("reorder changed IDs")
	}
}
func TestSubscriptionStaleSettingsCannotUndoRefreshOrRemoval(t *testing.T) {
	current := DefaultSettings()
	list, _, _ := parseSubscription(subscriptionFixture)
	_ = replaceSubscription(current, "a", list, false)
	current.Subscriptions = []Subscription{{ID: "a", URL: "https://private/token", Name: "A"}}
	stale := current.clone()
	current.Servers[0].Password = "latest"
	current.Subscriptions[0].Auto = true
	mergeSubscriptionSettings(stale, current)
	if stale.Servers[0].Password != "latest" || !stale.Subscriptions[0].Auto {
		t.Fatal("stale overwrite")
	}
	stale.Selected = stale.Servers[0].ID
	current.Servers = nil
	current.Subscriptions = nil
	mergeSubscriptionSettings(stale, current)
	if len(stale.Servers) != 0 || len(stale.Subscriptions) != 0 || stale.Selected != "auto" {
		t.Fatal("resurrected removed subscription")
	}
}
func TestSubscriptionMetadataDoesNotRestartCore(t *testing.T) {
	s := DefaultSettings()
	list, _, _ := parseSubscription(subscriptionFixture)
	_ = replaceSubscription(s, "a", list, false)
	before := s.fingerprint()
	s.Subscriptions = []Subscription{{ID: "a", URL: "https://private/token", Auto: true, Updated: 123, Error: "error"}}
	for i := range s.Servers {
		s.Servers[i].SubscriptionID = "different"
	}
	if before != s.fingerprint() {
		t.Fatal("metadata restarts core")
	}
}
func TestSubscriptionCommitAtomicAndPreviewOneUse(t *testing.T) {
	s := DefaultSettings()
	s.AutoApply = false
	a := &App{dir: t.TempDir(), settings: s, core: &Core{}}
	servers, _, _ := parseSubscription(subscriptionFixture)
	p := subscriptionPreview{sub: Subscription{ID: "a", Name: "A", URL: "https://private/token"}, servers: servers, expires: time.Now().Add(time.Minute)}
	a.subscriptions.previews = map[string]subscriptionPreview{"preview": p}
	request := func() *http.Request {
		return httptest.NewRequest("POST", "/api/subscriptions/add", strings.NewReader(`{"token":"preview"}`))
	}
	if _, e := a.apiSubscriptionAdd(request()); e != nil {
		t.Fatal(e)
	}
	if len(a.settings.Servers) != 2 || len(a.settings.Subscriptions) != 1 {
		t.Fatal(a.settings)
	}
	if _, e := a.apiSubscriptionAdd(request()); e == nil {
		t.Fatal("reused token")
	}
	saved, e := LoadSettings(a.dir)
	if e != nil || saved.Subscriptions[0].URL != p.sub.URL {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(saved)
	var value any
	_ = json.Unmarshal(raw, &value)
	red, _ := json.Marshal(redactSettings(value, ""))
	if strings.Contains(string(red), "private/token") {
		t.Fatal("report leaks URL")
	}
	block := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(block, []byte("x"), 0600)
	a.dir = filepath.Join(block, "child")
	before := a.settings.clone()
	_, e = a.commitSubscriptions(func(s *Settings) error { s.Subscriptions = nil; return nil })
	if e == nil || len(a.settings.Subscriptions) != len(before.Subscriptions) {
		t.Fatal("failed save committed")
	}
}
