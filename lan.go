package main

import (
	"net"
	"strings"
)

// lanIPs — адреса компьютера в домашней сети (без виртуальных адаптеров Naivka).
func lanIPs() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || strings.Contains(strings.ToLower(ifc.Name), "naivka") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() || ipn.IP.Equal(net.ParseIP("172.19.0.1")) {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	return out
}
