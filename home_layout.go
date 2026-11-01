package main

// Layout stores widget identifiers and grid spans, never executable HTML.
type HomeWidget struct {
	ID     string `json:"id"`
	Span   int    `json:"span"`
	Hidden bool   `json:"hidden"`
}
type HomeLayout struct {
	Version int          `json:"version"`
	Wide    []HomeWidget `json:"wide,omitempty"`
	Narrow  []HomeWidget `json:"narrow,omitempty"`
}

var homeWidgetIDs = []string{"status", "notices", "routes", "controls", "speed", "ip", "session", "ready", "activity"}

func (l *HomeLayout) normalize() {
	if l.Version != 1 {
		*l = HomeLayout{}
		return
	}
	clean := func(in []HomeWidget) []HomeWidget {
		out := []HomeWidget{}
		seen := map[string]bool{}
		for _, w := range in {
			known := false
			for _, id := range homeWidgetIDs {
				if w.ID == id {
					known = true
				}
			}
			if !known || seen[w.ID] {
				continue
			}
			seen[w.ID] = true
			if w.Span < 1 {
				w.Span = 1
			}
			if w.Span > 3 {
				w.Span = 3
			}
			if w.ID == "status" || w.ID == "notices" || w.ID == "routes" || w.ID == "controls" {
				w.Hidden = false
			}
			out = append(out, w)
		}
		for _, id := range homeWidgetIDs {
			if !seen[id] {
				span := 1
				if id == "status" || id == "routes" || id == "controls" || id == "notices" || id == "ready" {
					span = 3
				}
				out = append(out, HomeWidget{ID: id, Span: span})
			}
		}
		return out
	}
	l.Wide = clean(l.Wide)
	l.Narrow = clean(l.Narrow)
}
