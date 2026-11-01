package main

// Layout stores widget identifiers and grid spans, never executable HTML.
type HomeWidget struct {
	ID     string `json:"id"`
	Span   int    `json:"span"`
	Hidden bool   `json:"hidden"`
	Col    int    `json:"col,omitempty"`
	Row    int    `json:"row,omitempty"`
	Height int    `json:"height,omitempty"`
}
type HomeLayout struct {
	Version int          `json:"version"`
	Wide    []HomeWidget `json:"wide,omitempty"`
	Narrow  []HomeWidget `json:"narrow,omitempty"`
}

var homeWidgetIDs = []string{"status", "notices", "routes", "controls", "speed", "ip", "session", "ready", "activity"}

func (l *HomeLayout) normalize() {
	if l.Version != 1 && l.Version != 2 {
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
	place := func(in []HomeWidget, cols int) []HomeWidget {
		occupied := map[[2]int]bool{}
		fits := func(row, col, span int) bool {
			if row < 1 || row > 64 || col < 1 || col > cols || col+span-1 > cols {
				return false
			}
			for x := col; x < col+span; x++ {
				if occupied[[2]int{row, x}] {
					return false
				}
			}
			return true
		}
		for i := range in {
			w := &in[i]
			span := w.Span
			if cols == 1 {
				span = 1
			}
			if w.Height < 0 {
				w.Height = 0
			}
			if w.Height > 1200 {
				w.Height = 1200
			}
			if !fits(w.Row, w.Col, span) {
				found := false
				for y := 1; y <= 64 && !found; y++ {
					for x := 1; x <= cols; x++ {
						if fits(y, x, span) {
							w.Row, w.Col = y, x
							found = true
							break
						}
					}
				}
			}
			for x := w.Col; x < w.Col+span; x++ {
				occupied[[2]int{w.Row, x}] = true
			}
		}
		return in
	}
	l.Wide = place(clean(l.Wide), 3)
	l.Narrow = place(clean(l.Narrow), 1)
	l.Version = 2
}
