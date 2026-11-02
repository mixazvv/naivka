package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestRepairDNSUDPThroughLoopbackCore(t *testing.T) {
	tcp, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer tcp.Close()
	udp, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	udp.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		c, e := tcp.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		var hello [3]byte
		if _, e = io.ReadFull(c, hello[:]); e != nil {
			done <- e
			return
		}
		c.Write([]byte{5, 0})
		var request [10]byte
		if _, e = io.ReadFull(c, request[:]); e != nil {
			done <- e
			return
		}
		if request[1] != 3 {
			done <- io.ErrUnexpectedEOF
			return
		}
		p := udp.LocalAddr().(*net.UDPAddr).Port
		c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, byte(p >> 8), byte(p)})
		packet := make([]byte, 4096)
		n, source, e := udp.ReadFromUDP(packet)
		if e != nil {
			done <- e
			return
		}
		packet = packet[:n]
		// Request target must be the configured literal, with the fixed question.
		if n != 10+len(repairDNSQuestion()) || packet[3] != 1 || packet[4] != 8 || packet[7] != 8 || binary.BigEndian.Uint16(packet[8:10]) != 53 {
			done <- io.ErrUnexpectedEOF
			return
		}
		reply := append([]byte{}, repairDNSQuestion()...)
		reply[2] = 0x81
		reply[3] = 0x80
		reply[7] = 1
		reply = append(reply, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 93, 184, 216, 34)
		_, e = udp.WriteToUDP(append(packet[:10], reply...), source)
		done <- e
		io.Copy(io.Discard, c)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := repairTransportDNS(ctx, tcp.Addr().(*net.TCPAddr).Port, directDNS{Kind: "udp", Host: "8.8.8.8"})
	if got != "OK" {
		t.Fatal(got)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func TestRepairDNSNeverDialsExternalRelay(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(time.Second))
		var b [10]byte
		io.ReadFull(c, b[:3])
		c.Write([]byte{5, 0})
		io.ReadFull(c, b[:])
		c.Write([]byte{5, 0, 0, 1, 8, 8, 8, 8, 0, 53})
	}()
	got := repairTransportDNS(context.Background(), ln.Addr().(*net.TCPAddr).Port, directDNS{Kind: "udp", Host: "1.1.1.1"})
	if got != "INVALID_RESPONSE" {
		t.Fatal(got)
	}
}
func TestRepairDNSCancelledSocksHandshake(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		close(accepted)
		io.Copy(io.Discard, c)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-accepted; cancel() }()
	start := time.Now()
	repairTransportDNS(ctx, ln.Addr().(*net.TCPAddr).Port, directDNS{Kind: "tls", Host: "resolver.invalid"})
	if time.Since(start) > time.Second {
		t.Fatal("cancellation failed")
	}
}
func TestRepairDNSStrictAddresses(t *testing.T) {
	for _, ip := range []string{"169.254.1.1", "127.0.0.1", "192.168.0.1", "224.0.0.1"} {
		p := repairDNSQuestion()
		p[2] = 0x81
		p[3] = 0x80
		p[7] = 1
		p = append(p, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
		p = append(p, net.ParseIP(ip).To4()...)
		if got := repairDNSReply(p); got != "UNEXPECTED_ANSWER" {
			t.Fatal(ip, got)
		}
	}
}
