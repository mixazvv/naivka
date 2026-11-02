package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

func repairDNSQuestion() []byte {
	return []byte{0x4e, 0x56, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
}
func repairDNSError(err error) string {
	if e, ok := err.(net.Error); ok && e.Timeout() {
		return "TIMEOUT"
	}
	return "TRANSPORT_ERROR"
}
func repairSocksAddress(host string, port int) ([]byte, error) {
	var b []byte
	if ip := net.ParseIP(host); ip != nil {
		if v := ip.To4(); v != nil {
			b = append([]byte{1}, v...)
		} else {
			b = append([]byte{4}, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 253 {
			return nil, errors.New("DNS_HOST")
		}
		b = append([]byte{3, byte(len(host))}, host...)
	}
	return append(b, byte(port>>8), byte(port)), nil
}

// Only the loopback core is dialled. Names in CONNECT/ASSOCIATE are resolved by
// that core, never through an implicit operating-system DNS fallback.
func repairSocks(ctx context.Context, port int, command byte, address []byte) (net.Conn, []byte, error) {
	if port < 1 || port > 65535 {
		return nil, nil, errors.New("PROXY_PORT")
	}
	c, e := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if e != nil {
		return nil, nil, e
	}
	ok := false
	defer func() {
		if !ok {
			c.Close()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	if d, yes := ctx.Deadline(); yes && d.Before(deadline) {
		deadline = d
	}
	c.SetDeadline(deadline)
	stop := repairAfterCancel(ctx, func() { c.Close() })
	defer stop()
	if _, e = c.Write([]byte{5, 1, 0}); e != nil {
		return nil, nil, e
	}
	var h [4]byte
	if _, e = io.ReadFull(c, h[:2]); e != nil {
		return nil, nil, e
	}
	if h[0] != 5 || h[1] != 0 {
		return nil, nil, errors.New("SOCKS_AUTH")
	}
	if _, e = c.Write(append([]byte{5, command, 0}, address...)); e != nil {
		return nil, nil, e
	}
	if _, e = io.ReadFull(c, h[:]); e != nil {
		return nil, nil, e
	}
	if h[0] != 5 || h[1] != 0 || h[2] != 0 {
		return nil, nil, errors.New("SOCKS_REPLY")
	}
	size := 0
	switch h[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	default:
		return nil, nil, errors.New("SOCKS_RELAY_ADDRESS")
	}
	relay := make([]byte, size+2)
	if _, e = io.ReadFull(c, relay); e != nil {
		return nil, nil, e
	}
	ok = true
	return c, relay, nil
}
func repairTransportDNS(ctx context.Context, proxyPort int, d directDNS) string {
	if d.Kind == "local" {
		// This intentionally observes the OS resolver, which can follow TUN/NRPT.
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, "example.com")
		if e != nil {
			return repairDNSError(e)
		}
		for _, a := range ips {
			if a.IP.IsGlobalUnicast() && !a.IP.IsPrivate() {
				return "OK"
			}
		}
		return "UNEXPECTED_ANSWER"
	}
	if d.Kind != "tls" && d.Kind != "udp" {
		return "UNKNOWN"
	}
	port := d.Port
	if port == 0 {
		port = 53
		if d.Kind == "tls" {
			port = 853
		}
	}
	address, e := repairSocksAddress(d.Host, port)
	if e != nil {
		return "UNKNOWN"
	}
	command := byte(1)
	target := address
	if d.Kind == "udp" {
		command = 3
		target = []byte{1, 0, 0, 0, 0, 0, 0}
	}
	c, relay, e := repairSocks(ctx, proxyPort, command, target)
	if e != nil {
		return repairDNSError(e)
	}
	defer c.Close()
	stop := repairAfterCancel(ctx, func() { c.Close() })
	defer stop()
	question := repairDNSQuestion()
	if d.Kind == "tls" {
		secure := tls.Client(c, &tls.Config{ServerName: d.Host, MinVersion: tls.VersionTLS12})
		if e = secure.HandshakeContext(ctx); e != nil {
			return repairDNSError(e)
		}
		b := append([]byte{0, byte(len(question))}, question...)
		if _, e = secure.Write(b); e != nil {
			return repairDNSError(e)
		}
		var size [2]byte
		if _, e = io.ReadFull(secure, size[:]); e != nil {
			return repairDNSError(e)
		}
		n := int(binary.BigEndian.Uint16(size[:]))
		if n > 4096 {
			return "INVALID_RESPONSE"
		}
		packet := make([]byte, n)
		if _, e = io.ReadFull(secure, packet); e != nil {
			return repairDNSError(e)
		}
		return repairDNSReply(packet)
	}
	// The UDP relay must be the loopback core, never an address supplied by a
	// remote SOCKS endpoint. No local DNS resolution is used for the relay.
	ip := net.IP(relay[:len(relay)-2])
	if ip.IsUnspecified() {
		ip = net.ParseIP("127.0.0.1")
	}
	if !ip.IsLoopback() {
		return "INVALID_RESPONSE"
	}
	relayPort := int(binary.BigEndian.Uint16(relay[len(relay)-2:]))
	if relayPort == 0 {
		return "INVALID_RESPONSE"
	}
	udp, e := net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: relayPort})
	if e != nil {
		return repairDNSError(e)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(5 * time.Second))
	stopUDP := repairAfterCancel(ctx, func() { udp.Close() })
	defer stopUDP()
	if _, e = udp.Write(append(append([]byte{0, 0, 0}, address...), question...)); e != nil {
		return repairDNSError(e)
	}
	packet := make([]byte, 4200)
	n, e := udp.Read(packet)
	if e != nil {
		return repairDNSError(e)
	}
	packet = packet[:n]
	if n < 3+len(address) || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return "INVALID_RESPONSE"
	}
	// UDP destination is a configured literal IP. Match the returned source tuple.
	for i, v := range address {
		if packet[3+i] != v {
			return "INVALID_RESPONSE"
		}
	}
	return repairDNSReply(packet[3+len(address):])
}

func repairAfterCancel(ctx context.Context, f func()) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			f()
		case <-done:
		}
	}()
	return func() { close(done) }
}
