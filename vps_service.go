//go:build !legacy

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

var vpsServices = map[string]bool{"sing-box": true, "xray": true, "naivka-caddy": true, "naivka-mita": true, "x-ui": true, "s-ui": true}

type vpsServiceReq struct {
	friendsReq
	Service string `json:"service"`
	Confirm bool   `json:"confirm"`
}
type vpsServiceResult struct {
	Service   string `json:"service"`
	Action    string `json:"action"`
	State     string `json:"state,omitempty"`
	Log       string `json:"log,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	CheckedAt int64  `json:"checkedAt"`
}

func (r vpsServiceReq) validate() error {
	if !vpsServices[r.Service] {
		return errors.New("Эта служба не поддерживается для управления VPS")
	}
	switch r.Action {
	case "logs":
		return nil
	case "start", "stop", "restart":
		if !r.Confirm {
			return errors.New("Подтвердите действие со службой VPS")
		}
		return nil
	}
	return errors.New("Неизвестное действие со службой VPS")
}

// Fixed script, fixed units and operations. No shell command/path comes from the client.
const vpsServiceScript = `export LC_ALL=C
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
service=$1; action=$2
case "$service" in sing-box|xray|naivka-caddy|naivka-mita|x-ui|s-ui) ;; *) exit 2;; esac
[ "$(systemctl show "$service" -p LoadState --value 2>/dev/null)" = loaded ] || exit 3
case "$action" in
 logs) exec journalctl --unit="$service" --lines=100 --no-pager --output=short-iso;;
 start|stop|restart)
   exec 8>/etc/sing-box.install.lock || exit 4
   flock -n 8 || exit 4
   systemctl "$action" "$service" || exit 5
   state=$(systemctl is-active "$service" 2>/dev/null)
   case "$state" in active|inactive|failed|activating|deactivating) printf '%s\n' "$state";; *) printf 'unknown\n';; esac;;
 *) exit 2;;
esac
`

var vpsLogURL = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"']+`)
var vpsLogSecret = regexp.MustCompile(`(?i)(password|passwd|token|secret|private[_ -]?key|public[_ -]?key|authorization|uuid|short[_ -]?id)["']?\s*[:=]\s*("[^"\n]*"|'[^'\n]*'|[^\s,;]+)`)
var vpsLogAuthorization = regexp.MustCompile(`(?im)authorization["']?\s*[:=][^\n]+`)

var vpsLogUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
var vpsLogKey = regexp.MustCompile(`\b[A-Za-z0-9_-]{40,}={0,2}\b`)

func cleanVPSLog(raw string) string {
	raw = ansiRe2.ReplaceAllString(raw, "")
	raw = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, raw)
	raw = vpsLogURL.ReplaceAllString(raw, "[ссылка скрыта]")
	raw = vpsLogAuthorization.ReplaceAllString(raw, "authorization=[скрыто]")
	raw = vpsLogSecret.ReplaceAllString(raw, "$1=[скрыто]")
	raw = vpsLogUUID.ReplaceAllString(raw, "[UUID скрыт]")
	return vpsLogKey.ReplaceAllString(raw, "[ключ скрыт]")
}

func executeVPSService(ctx context.Context, cl *ssh.Client, req vpsServiceReq) (vpsServiceResult, error) {
	type response struct {
		out vpsOutput
		err error
	}
	done := make(chan response, 1)
	go func() {
		session, err := cl.NewSession()
		if err != nil {
			done <- response{err: err}
			return
		}
		defer session.Close()
		var out vpsOutput
		session.Stdout = &out
		session.Stderr = io.Discard
		session.Stdin = strings.NewReader(vpsServiceScript)
		args := shellQuote(req.Service) + " " + shellQuote(req.Action)
		err = session.Run("if [ \"$(id -u)\" = 0 ]; then sh -s -- " + args + "; else sudo -n sh -s -- " + args + "; fi")
		done <- response{out, err}
	}()
	select {
	case <-ctx.Done():
		cl.Close()
		return vpsServiceResult{}, errors.New("Ожидание VPS завершилось. Действие могло выполниться; обновите состояние службы перед повтором")
	case res := <-done:
		if res.err != nil {
			return vpsServiceResult{}, errors.New("Не удалось выполнить действие: проверьте наличие службы, права root/sudo без пароля и отсутствие установки на VPS")
		}
		result := vpsServiceResult{Service: req.Service, Action: req.Action, CheckedAt: time.Now().Unix()}
		if req.Action == "logs" {
			result.Log = cleanVPSLog(string(res.out.data))
			result.Truncated = res.out.overflow
		} else {
			state := strings.TrimSpace(string(res.out.data))
			switch state {
			case "active", "inactive", "failed", "activating", "deactivating":
				result.State = state
			default:
				result.State = "unknown"
			}
		}
		return result, nil
	}
}

func (a *App) vpsServiceRoutes(api func(method, path string, f apiFunc)) {
	api("POST", "/api/vps/service", func(r *http.Request) (any, error) {
		var req vpsServiceReq
		if err := decode(r, &req); err != nil {
			return nil, err
		}
		if err := req.validate(); err != nil {
			return nil, err
		}
		if req.Action != "logs" && a.installer != nil {
			a.installer.mu.Lock()
			installing := a.installer.state.Active
			a.installer.mu.Unlock()
			if installing {
				return nil, errors.New("Сначала дождитесь завершения установки VPS")
			}
		}
		host, ok := a.ownHost(req.Host)
		if !ok {
			return nil, errors.New("Сервер не найден")
		}
		if !a.friends.ops.TryLock() {
			return nil, errors.New("SSH занят другой операцией. Повторите позже")
		}
		defer a.friends.ops.Unlock()
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		cl, err := a.friendsClient(host, req.friendsReq)
		if errors.Is(err, errNeedAuth) {
			return map[string]bool{"needAuth": true}, nil
		}
		if err != nil {
			return nil, err
		}
		defer a.friends.release(host.ID, cl)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		result, err := executeVPSService(ctx, cl, req)
		if err != nil {
			a.friends.drop(host.ID, cl)
			return nil, err
		}
		return result, nil
	})
}
