package main

import "strings"

// singboxFingerprint translates known uTLS/Xray names to sing-box's browser
// presets when generating its config. Keep the subscription's original value
// in Server: Xray accepts detailed names, and exports/refreshes must retain it.
// Do not guess a browser for unknown/version-specific profiles or disable uTLS.
func singboxFingerprint(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "":
		return "chrome"
	case "chrome", "firefox", "edge", "safari", "ios", "android", "360", "qq", "random", "randomized",
		"chrome_psk", "chrome_psk_shuffle", "chrome_padding_psk_shuffle", "chrome_pq", "chrome_pq_psk":
		return n
	case "hellochrome_auto":
		return "chrome"
	case "hellofirefox_auto", "hellofirefox_120", "firefox120":
		// sing-box's metacubex/utls v1.8.7 maps HelloFirefox_Auto to HelloFirefox_120.
		return "firefox"
	case "helloedge_auto":
		return "edge"
	case "hellosafari_auto":
		return "safari"
	case "helloios_auto":
		return "ios"
	case "helloandroid_11_okhttp":
		return "android"
	case "hello360_auto":
		return "360"
	case "helloqq_auto":
		return "qq"
	case "hellorandomized":
		return "randomized"
	default:
		return name
	}
}
