package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSubscriptionEventsCommittedChanges(t *testing.T) {
	before := DefaultSettings()
	before.Subscriptions = []Subscription{{ID: "one", Name: "private-secret", URL: "https://private-secret", Updated: 1, Attempt: 1}}
	before.Servers = []Server{{ID: "a", Name: "private-secret", Host: "a.invalid", Type: "trojan", Password: "private-secret", SubscriptionID: "one"}}
	after := before.clone()
	if got := subscriptionEvents(before, after); len(got) != 0 {
		t.Fatal(got)
	}
	after.Subscriptions[0].Auto = true
	if got := subscriptionEvents(before, after); len(got) != 0 {
		t.Fatal("auto toggle emitted update", got)
	}
	after.Subscriptions[0].Updated = 2
	after.Servers = append(after.Servers, Server{ID: "b", Name: "private-secret", Host: "b.invalid", Type: "trojan", Password: "private-secret", SubscriptionID: "one"})
	got := subscriptionEvents(before, after)
	if len(got) != 1 || got[0].Kind != "ok" || !strings.Contains(got[0].Detail, "Добавлено: 1") {
		t.Fatal(got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "private-secret") {
		t.Fatal("secret leaked", string(b))
	}
	before = after.clone()
	after.Subscriptions[0].Error = "private-secret"
	after.Subscriptions[0].Attempt = 3
	got = subscriptionEvents(before, after)
	b, _ = json.Marshal(got)
	if len(got) != 1 || got[0].Kind != "warn" || strings.Contains(string(b), "private-secret") {
		t.Fatal(got)
	}
	if len(subscriptionEvents(after, after)) != 0 {
		t.Fatal("duplicate error")
	}
	before = after.clone()
	after.Subscriptions = nil
	after.Servers = nil
	got = subscriptionEvents(before, after)
	if len(got) != 1 || got[0].Kind != "info" {
		t.Fatal(got)
	}
}
