module naivka

go 1.24.0

// Зеркала golang.org/x/sys и x/crypto на GitHub: так сборка не зависит от proxy.golang.org.
replace golang.org/x/sys => github.com/golang/sys v0.41.0

replace golang.org/x/crypto => github.com/golang/crypto v0.48.0

require github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808

require (
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	golang.org/x/crypto v0.48.0
	golang.org/x/sys v0.41.0 // indirect
)
