module azerothcore-autobot

go 1.26.4

require (
	github.com/azerothcore/AzerothGhost v1.0.9
	github.com/go-sql-driver/mysql v1.10.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/o0olele/detour-go v0.0.0-20260624174442-8056537019f8 // indirect
)

// This redirect is needed as soon as a detour-backed AzerothGhost package
// (navmesh/movement) is imported: its go.mod points o0olele/detour-go at
// walkline/detour-go, and replace directives do not propagate between modules.
// Until then `go mod tidy` keeps the replace but drops the matching require,
// because the module graph is pruned to the packages actually imported.
//
// Do not delete it: without it, the first such import fails with
// `o0olele/detour-go@v0.0.0-...: invalid version: unknown revision 8056537019f8`.
replace github.com/o0olele/detour-go => github.com/walkline/detour-go v0.0.0-20260624174442-8056537019f8
