module azerothcore-autobot

go 1.26.4

require (
	github.com/azerothcore/AzerothGhost v1.0.9
	github.com/go-sql-driver/mysql v1.10.0
	gopkg.in/yaml.v3 v3.0.1
)

// AzerothGhost depends on github.com/o0olele/detour-go but redirects it via a
// `replace` in its own go.mod. `replace` directives do not propagate across
// modules, so the redirect must be repeated here. Without these two lines
// `go mod tidy` fails with:
//
//	github.com/o0olele/detour-go@v0.0.0-...: invalid version: unknown revision 8056537019f8
require github.com/o0olele/detour-go v0.0.0-20260624174442-8056537019f8 // indirect

replace github.com/o0olele/detour-go => github.com/walkline/detour-go v0.0.0-20260624174442-8056537019f8
