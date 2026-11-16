package main

import "fmt"

// Events are built from the committed transaction, never from a downloaded
// preview. No subscription URL, profile, credential or user-provided name is logged.
func subscriptionEvents(before, after *Settings) []Event {
	old := make(map[string]Subscription)
	for _, sub := range before.Subscriptions {
		old[sub.ID] = sub
	}
	events := []Event{}
	for i, sub := range after.Subscriptions {
		prev, exists := old[sub.ID]
		delete(old, sub.ID)
		label := fmt.Sprintf("Подписка №%d", i+1)
		if sub.Error != "" {
			if !exists || sub.Error != prev.Error || sub.Attempt != prev.Attempt {
				events = append(events, Event{Kind: "warn", Text: label + ": не удалось обновить; прежние серверы сохранены", Detail: "Причина доступна в карточке подписки в разделе «Серверы»."})
			}
			continue
		}
		diff := diffSubscription(subscriptionServers(before, sub.ID), subscriptionServers(after, sub.ID))
		if exists && sub.Updated == prev.Updated && sub.Attempt == prev.Attempt && sub.Error == prev.Error && len(diff.Added)+len(diff.Removed)+len(diff.Changed) == 0 {
			continue
		}
		title := label + ": обновлена"
		if !exists {
			title = label + ": добавлена"
		}
		events = append(events, Event{Kind: "ok", Text: title, Detail: fmt.Sprintf("Добавлено: %d · удалено: %d · изменено: %d · без изменений: %d", len(diff.Added), len(diff.Removed), len(diff.Changed), diff.Unchanged)})
	}
	if len(old) > 0 {
		events = append(events, Event{Kind: "info", Text: fmt.Sprintf("Удалено подписок: %d", len(old))})
	}
	return events
}
