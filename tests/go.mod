module github.com/unstablebuild/rune-cursor-pack/tests

go 1.22

require github.com/hajimehoshi/ebiten/v2 v2.7.4

require (
	github.com/ebitengine/gomobile v0.0.0-20240518074828-e86332849895 // indirect
	github.com/ebitengine/hideconsole v1.0.0 // indirect
	github.com/ebitengine/purego v0.8.3 // indirect
	github.com/jezek/xgb v1.1.1 // indirect
	golang.org/x/sync v0.7.0 // indirect
	golang.org/x/sys v0.20.0 // indirect
)

// Match Rune's Kage compiler and graphics backend.
replace github.com/hajimehoshi/ebiten/v2 => github.com/unstablebuild/ebiten/v2 v2.7.5-ub.33
