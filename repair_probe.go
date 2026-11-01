package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

func repairClient(port int) *http.Client {
	if port < 1 || port > 65535 {
		return nil
	}
	// The sole dial target is the configured loopback proxy. Never fall back to
	// DefaultTransport, environment proxy settings or a direct client on failure.
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}), TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 5 * time.Second}
}
func repairHTTP(ctx context.Context, client *http.Client, endpoint string) string {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return "UNKNOWN"
	}
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return "TIMEOUT"
		}
		return "TRANSPORT_ERROR"
	}
	if resp.StatusCode == 204 {
		return "OK"
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "REDIRECT"
	}
	return "UNEXPECTED_HTTP"
}

// DNS wire response for the fixed example.com/A question. Validate the ID, QR,
// question, RCODE and presence of an answer; never expose packet contents.
func repairDNSReply(packet []byte) string {
	question := []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	if len(packet) < 12+len(question) || packet[0] != 0x4e || packet[1] != 0x56 || packet[2]&0x80 == 0 || packet[2]&0x78 != 0 || packet[2]&2 != 0 || binary.BigEndian.Uint16(packet[4:6]) != 1 || !bytes.Equal(packet[12:12+len(question)], question) {
		return "INVALID_RESPONSE"
	}
	switch packet[3] & 15 {
	case 3:
		return "NXDOMAIN"
	case 0:
	default:
		return "DNS_ERROR"
	}
	count := int(binary.BigEndian.Uint16(packet[6:8]))
	if count > 64 {
		return "INVALID_RESPONSE"
	}
	pos := 12 + len(question)
	hasAnswer := false
	for i := 0; i < count; i++ {
		// Skip a bounded owner name without following compression pointer chains.
		ended := false
		for labels := 0; labels < 128 && pos < len(packet); labels++ {
			length := int(packet[pos])
			pos++
			if length == 0 {
				ended = true
				break
			}
			if length&0xc0 == 0xc0 {
				if pos >= len(packet) || ((length&63)<<8|int(packet[pos])) >= len(packet) {
					return "INVALID_RESPONSE"
				}
				pos++
				ended = true
				break
			}
			if length > 63 || pos+length > len(packet) {
				return "INVALID_RESPONSE"
			}
			pos += length
		}
		if !ended || pos+10 > len(packet) {
			return "INVALID_RESPONSE"
		}
		kind := binary.BigEndian.Uint16(packet[pos : pos+2])
		class := binary.BigEndian.Uint16(packet[pos+2 : pos+4])
		size := int(binary.BigEndian.Uint16(packet[pos+8 : pos+10]))
		pos += 10
		if pos+size > len(packet) {
			return "INVALID_RESPONSE"
		}
		if kind == 1 && class == 1 && size == 4 {
			ip := net.IP(packet[pos : pos+4])
			if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsMulticast() {
				return "UNEXPECTED_ANSWER"
			}
			hasAnswer = true
		}
		pos += size
	}
	if hasAnswer {
		return "OK"
	}
	return "NO_ANSWER"
}

func repairDNS(ctx context.Context, client *http.Client, endpoint string) string {
	if endpoint == "" {
		return "UNKNOWN"
	}
	packet := []byte{0x4e, 0x56, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(packet))
	if err != nil {
		return "UNKNOWN"
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return "TIMEOUT"
		}
		return "TRANSPORT_ERROR"
	}
	if resp.StatusCode != 200 {
		return "DNS_HTTP_ERROR"
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return "TIMEOUT"
		}
		return "INVALID_RESPONSE"
	}
	if len(raw) > 4096 {
		return "INVALID_RESPONSE"
	}
	return repairDNSReply(raw)
}
func probeRepairPath(ctx context.Context, port int, doh string) repairPath {
	client := repairClient(port)
	if client == nil {
		return repairPath{DNS: "UNKNOWN"}
	}
	var result repairPath
	for round := 0; round < 2; round++ {
		var wg sync.WaitGroup
		wg.Add(3)
		for i, endpoint := range []string{"https://www.gstatic.com/generate_204", "https://cp.cloudflare.com/generate_204"} {
			go func(i int, u string) { defer wg.Done(); result.HTTP[i] = repairHTTP(ctx, client, u) }(i, endpoint)
		}
		go func() { defer wg.Done(); result.DNS = repairDNS(ctx, client, doh) }()
		wg.Wait()
		if result.verified() || ctx.Err() != nil {
			break
		}
		if round == 0 {
			select {
			case <-ctx.Done():
				return result
			case <-time.After(time.Second):
			}
		}
	}
	return result
}
