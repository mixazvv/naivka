package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSubscriptionDeviceIdentityPersistence(t *testing.T) {
	dir := t.TempDir()
	id, e := subscriptionDeviceID(dir, "https://provider.example/token")
	if e != nil || !regexp.MustCompile(`^[a-zA-Z0-9=-]{10,64}$`).MatchString(id) {
		t.Fatal(id, e)
	}
	again, _ := subscriptionDeviceID(dir, "https://PROVIDER.example/rotated-token")
	other, _ := subscriptionDeviceID(dir, "https://other.example/token")
	installation, _ := subscriptionDeviceID(t.TempDir(), "https://provider.example/token")
	if id != again || id == other || id == installation {
		t.Fatal("identity scope or persistence violated")
	}
	if e := os.WriteFile(filepath.Join(dir, "subscription-device-id"), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := subscriptionDeviceID(dir, "https://provider.example/token"); e == nil {
		t.Fatal("corrupt identity silently rotated")
	}
}

func TestSubscriptionClientHeadersAndRedirectPrivacy(t *testing.T) {
	for _, mode := range []string{"", "device", "happ"} {
		for _, crossOrigin := range []bool{false, true} {
			t.Run(mode+map[bool]string{true: "/cross", false: "/same"}[crossOrigin], func(t *testing.T) {
				calls := 0
				opt := subscriptionFetchOptions{client: mode}
				if mode != "" {
					opt.deviceID = strings.Repeat("a", 64)
				}
				hc := &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					wantID := opt.deviceID
					if calls > 1 && crossOrigin {
						wantID = ""
					}
					if r.Header.Get("x-hwid") != wantID {
						t.Fatal("identity missing or disclosed across origins")
					}
					if mode == "happ" && r.UserAgent() != "Happ/3.23.0" {
						t.Fatal(r.UserAgent())
					}
					if wantID != "" && r.Header.Get("x-device-os") != "Windows" {
						t.Fatal("missing OS")
					}
					if calls == 1 {
						location := "https://provider.example/redirected"
						if crossOrigin {
							location = "https://other.example/redirected"
						}
						return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{location}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(subscriptionFixture)), Request: r}, nil
				})}
				list, _, e := fetchSubscription(context.Background(), hc, "https://provider.example/token", opt)
				if e != nil || len(list) != 2 || calls != 2 {
					t.Fatal(e, calls, len(list))
				}
			})
		}
	}
}

func TestSubscriptionProviderRefusalBeforeParsing(t *testing.T) {
	for _, status := range []int{200, 403, 404} {
		for _, header := range []string{"x-hwid-not-supported", "x-hwid-max-devices-reached", "x-hwid-limit"} {
			calls := 0
			hc := &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				h := make(http.Header)
				h.Set(header, "true")
				return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(subscriptionFixture)), Request: r}, nil
			})}
			list, _, e := fetchAvailableSubscription(context.Background(), []*http.Client{hc, hc}, "https://provider.example/private-token")
			if e == nil || len(list) != 0 || calls != 1 || strings.Contains(e.Error(), "private-token") {
				t.Fatal(e, calls, len(list))
			}
		}
	}
}

func TestSubscriptionClientSavedWithoutChangingCore(t *testing.T) {
	s := DefaultSettings()
	s.AutoApply = false
	s.normalize()
	s.Subscriptions = []Subscription{{ID: "sub", URL: "https://provider.example/token"}}
	a := &App{dir: t.TempDir(), settings: s, core: &Core{}}
	before := s.fingerprint()
	_, e := a.apiSubscriptionClient(httptest.NewRequest("POST", "/api/subscriptions/client", strings.NewReader(`{"id":"sub","client":"happ"}`)))
	if e != nil {
		t.Fatal(e)
	}
	saved, e := LoadSettings(a.dir)
	if e != nil || saved.Subscriptions[0].Client != "happ" || before != a.settings.fingerprint() {
		t.Fatal("mode not persisted or restarted core", e)
	}
	stale := s.clone()
	mergeSubscriptionSettings(stale, a.settings)
	if stale.Subscriptions[0].Client != "happ" {
		t.Fatal("stale form lost mode")
	}
	_, e = a.apiSubscriptionClient(httptest.NewRequest("POST", "/api/subscriptions/client", strings.NewReader(`{"id":"sub","client":"bad"}`)))
	if e == nil || a.settings.Subscriptions[0].Client != "happ" {
		t.Fatal("invalid mode accepted")
	}
}

func TestSubscriptionForbiddenNegotiation(t *testing.T) {
	calls := 0
	hc := &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		status := 200
		if r.UserAgent() == "Naivka" {
			status = 403
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(subscriptionFixture)), Request: r}, nil
	})}
	list, _, e := fetchSubscription(context.Background(), hc, "https://provider.example/token")
	if e != nil || len(list) != 2 || calls != 2 {
		t.Fatal(e, calls)
	}
}

type subscriptionBrokenBody struct{}

func (subscriptionBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (subscriptionBrokenBody) Close() error             { return nil }

func TestSubscriptionBodyReadErrorExplainsPhase(t *testing.T) {
	hc := &http.Client{Transport: subscriptionTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: subscriptionBrokenBody{}, Request: r}, nil
	})}
	_, _, e := fetchSubscription(context.Background(), hc, "https://provider.example/private-token")
	if e == nil || !strings.Contains(e.Error(), "HTTP 200, чтение ответа") || !strings.Contains(e.Error(), "EOF") || strings.Contains(e.Error(), "private-token") {
		t.Fatal(e)
	}
}
