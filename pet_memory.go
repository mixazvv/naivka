package main

import "time"

type PetMemory struct {
	Version   int              `json:"version"`
	Created   int64            `json:"created"`
	LastEvent int64            `json:"lastEvent"`
	Visits    map[string]int   `json:"visits,omitempty"`
	Events    map[string]int64 `json:"events,omitempty"`
}

var petSurfaceIDs = map[string]bool{"lanesCard": true, "tileSpeed": true, "tileIp": true, "tileSession": true, "stageReady": true, "liveCol": true, "homeServer": true, "chartBox": true}
var petEventIDs = map[string]bool{"night-star": true, "stable-session": true, "toy": true, "anniversary": true}

func (m *PetMemory) normalize() {
	if m.Version != 1 {
		*m = PetMemory{}
		return
	}
	now := time.Now().Unix()
	if m.Created < 0 || m.Created > now {
		m.Created = 0
	}
	if m.LastEvent < 0 || m.LastEvent > now {
		m.LastEvent = 0
	}
	visits := map[string]int{}
	for id, n := range m.Visits {
		if petSurfaceIDs[id] && n > 0 {
			if n > 1000 {
				n = 1000
			}
			visits[id] = n
		}
	}
	m.Visits = visits
	events := map[string]int64{}
	for id, at := range m.Events {
		if petEventIDs[id] && at > 0 && at <= now {
			events[id] = at
		}
	}
	m.Events = events
}
