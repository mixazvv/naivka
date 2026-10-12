module naivka

go 1.22

// Зеркало golang.org/x/sys на GitHub: так сборка не зависит от proxy.golang.org.
replace golang.org/x/sys => github.com/golang/sys v0.30.0

require github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808

require (
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	golang.org/x/sys v0.0.0-20210218145245-beda7e5e158e // indirect
)
