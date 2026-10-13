package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Тест скорости: задержка (у всех маршрутов сразу) и скорость загрузки (по очереди) напрямую и через каждый сервер.
// Каждый маршрут идёт через свой служебный вход sing-box, поэтому основной трафик не трогается.

type SpeedRow struct {
	Key   string  `json:"key"`
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`  // direct | server | wl
	State string  `json:"state"` // wait | run | ping (задержка есть, скорость в очереди) | done | error
	Ping  int     `json:"ping"`  // мс
	Mbps  float64 `json:"mbps"`
	Error string  `json:"error,omitempty"`
}

type SpeedState struct {
	Active bool       `json:"active"`
	Rows   []SpeedRow `json:"rows"`
	At     int64      `json:"at"`
}

var (
	speedMu sync.Mutex
	speed   SpeedState
)

var (
	pingURLs = []string{"https://cp.cloudflare.com/generate_204", "https://www.gstatic.com/generate_204"}
	downURLs = []string{"https://speed.cloudflare.com/__down?bytes=50000000", "https://nbg1-speed.hetzner.com/100MB.bin"}
)

func (a *App) StartSpeedTest() error {
	speedMu.Lock()
	if speed.Active {
		speedMu.Unlock()
		return nil
	}
	s := a.settingsCopy()
	directPort, _ := a.core.ProbePorts()
	rows := []SpeedRow{{Key: "direct", Name: "Напрямую", Kind: "direct", State: "wait"}}
	ports := map[string]int{"direct": directPort}
	probes := a.serverProbes()
	for _, sv := range s.Servers {
		if p := probes[serverTag(sv)]; p > 0 {
			rows = append(rows, SpeedRow{Key: serverTag(sv), Name: sv.Name, Kind: "server", State: "wait"})
			ports[serverTag(sv)] = p
		}
	}
	if p := probes["wl"]; p > 0 {
		rows = append(rows, SpeedRow{Key: "wl", Name: "Белые списки (лучший сервер)", Kind: "wl", State: "wait"})
		ports["wl"] = p
	}
	speed = SpeedState{Active: true, Rows: rows, At: time.Now().Unix()}
	speedMu.Unlock()

	go func() {
		set := func(i int, f func(r *SpeedRow)) { speedMu.Lock(); f(&speed.Rows[i]); speedMu.Unlock() }
		// Задержка — сразу у всех маршрутов параллельно: запросы маленькие и друг другу не мешают.
		var wg sync.WaitGroup
		for i := range rows {
			set(i, func(r *SpeedRow) { r.State = "run" })
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ping, err := measurePing(ports[rows[i].Key])
				set(i, func(r *SpeedRow) {
					r.Ping, r.State = ping, "ping"
					if err != nil {
						r.State, r.Error = "error", humanErr(err)
					}
				})
			}(i)
		}
		wg.Wait()
		// Скорость — по очереди: параллельные загрузки делили бы один канал и занижали цифры.
		for i := range rows {
			speedMu.Lock()
			skip := speed.Rows[i].State == "error"
			speedMu.Unlock()
			if skip {
				continue
			}
			set(i, func(r *SpeedRow) { r.State = "run" })
			mbps, err := measureDown(ports[rows[i].Key])
			set(i, func(r *SpeedRow) {
				r.Mbps, r.State = mbps, "done"
				if err != nil {
					r.State, r.Error = "error", humanErr(err)
				}
			})
		}
		speedMu.Lock()
		speed.Active = false
		speedMu.Unlock()
		a.mon.Event("info", "Тест скорости завершён", false)
	}()
	return nil
}

func speedView() SpeedState {
	speedMu.Lock()
	defer speedMu.Unlock()
	v := speed
	v.Rows = append([]SpeedRow(nil), speed.Rows...)
	return v
}

// measurePing: медиана четырёх запросов после разогрева (соединение уже открыто,
// так меряется путь, а не рукопожатие).
func measurePing(port int) (int, error) {
	hc := geoClient(port)
	hc.Timeout = 10 * time.Second
	var err error
	for _, u := range pingURLs {
		var ms []int
		for i := 0; i < 5; i++ {
			start := time.Now()
			resp, e := hc.Get(u)
			if e != nil {
				err = e
				break
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if i > 0 {
				ms = append(ms, int(time.Since(start)/time.Millisecond))
			}
		}
		if len(ms) >= 2 {
			sort.Ints(ms)
			return ms[len(ms)/2], nil
		}
	}
	return 0, err
}

// downFor — сколько секунд качать на маршрут: этого хватает, чтобы канал разогнался.
const downFor = 5 * time.Second

// measureDown: скорость загрузки за downFor.
func measureDown(port int) (mbps float64, err error) {
	hc := geoClient(port)
	for _, u := range downURLs {
		ctx, cancel := context.WithTimeout(context.Background(), downFor+4*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		resp, e := hc.Do(req)
		if e != nil {
			cancel()
			err = e
			continue
		}
		start := time.Now()
		var n int64
		buf := make([]byte, 64<<10)
		for time.Since(start) < downFor {
			k, re := resp.Body.Read(buf)
			n += int64(k)
			if re != nil {
				break
			}
		}
		el := time.Since(start).Seconds()
		resp.Body.Close()
		cancel()
		if n > 256<<10 && el > 0 {
			return float64(n) * 8 / el / 1e6, nil
		}
		err = errors.New("слишком мало данных")
	}
	return 0, err
}
