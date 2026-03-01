# Building wbgo.so

`wbgo.so` is a Go plugin (`-buildmode=plugin`) that provides the MQTT/driver layer
for wb-rules. It is built from the private repository `github.com/wirenboard/wbgo-private`
and loaded at runtime via `plugin.Open()` in the `wbgong` library.

## Plugin loading

wb-rules loads `wbgo.so` at startup from `/usr/lib/wb-rules/wbgo.so` (or the path
specified by the `-wbgo` flag). If the plugin fails to load, the process exits
with `ERROR in init wbgo.so`.

## Building from source

Requires access to `github.com/wirenboard/wbgo-private`.

```bash
cd wbgo-private

# amd64 (for running tests on the build host)
go build -buildmode=plugin -o amd64.wbgo.so .

# arm64 (Wiren Board 7/8)
GOARCH=arm64 CC=aarch64-linux-gnu-gcc CGO_ENABLED=1 \
  go build -buildmode=plugin -o arm64.wbgo.so .

# armhf (Wiren Board 5/6)
GOARCH=arm GOARM=6 CC=arm-linux-gnueabihf-gcc CGO_ENABLED=1 \
  go build -buildmode=plugin -o armhf.wbgo.so .
```

Place the resulting `.wbgo.so` files in the wb-rules repo root. The Makefile
uses them:

- `make test` — copies `amd64.wbgo.so` → `wbrules/wbgo.so` and runs tests
- `make install` — installs `$(DEB_TARGET_ARCH).wbgo.so` → `/usr/lib/wb-rules/wbgo.so`

## Extracting from a published deb

If you don't have access to `wbgo-private`, extract the pre-built plugin from
a published wb-rules deb package:

```bash
wget https://deb.wirenboard.com/wb8/bullseye/pool/main/w/wb-rules/wb-rules_VERSION_arm64.deb
dpkg-deb -x wb-rules_VERSION_arm64.deb tmp/
cp tmp/usr/lib/wb-rules/wbgo.so arm64.wbgo.so
```

## Version matching constraints

Go plugins are extremely sensitive to build environment. **All of the following
must match exactly** between `wbgo.so` and the `wb-rules` binary:

### Go toolchain version

The Go compiler version used to build `wbgo.so` must match the one used for
`wb-rules`. The Debian build uses Go 1.21 (`debian/rules` sets
`GO=/usr/lib/go-1.21/bin/go`), while `go.mod` specifies Go 1.20 as the
minimum language version.

### Dependency versions

Every shared dependency must be at the exact same version in both the plugin
and the host binary. Mismatches cause runtime errors like:

```
plugin was built with a different version of package golang.org/x/sys/unix
```

The most common offender is **`golang.org/x/sys`** — it is an indirect
dependency pulled in by `fsnotify`, `wbgong`, and other packages. If you
update any dependency in `wb-rules` that transitively pulls a different
`x/sys` version, `wbgo.so` will fail to load until it is rebuilt against
the same version.

Current pinned versions (from `go.mod`):

| Package | Version | Notes |
|---------|---------|-------|
| `github.com/wirenboard/wbgong` | v0.6.0 | Must match between plugin and binary |
| `golang.org/x/sys` | v0.22.0 | Indirect; must match exactly |
| `github.com/wirenboard/go-duktape` | v0.0.0-20240729... | Duktape JS engine bindings |

### Practical advice

- **Never import `golang.org/x/sys/unix` directly** in wb-rules code. Use
  `syscall` (stdlib) instead. Importing `x/sys/unix` directly creates a
  hard coupling that breaks when the plugin was built against a different
  `x/sys` version.
- After running `go mod tidy`, check that `golang.org/x/sys` version hasn't
  changed. If it has, the plugin must be rebuilt.
- After updating `wbgong`, rebuild the plugin from `wbgo-private` with the
  matching `wbgong` version.
- `go mod tidy` may bump the Go language version in `go.mod` (e.g., 1.20 → 1.24).
  Prevent this with: `GOFLAGS=-mod=mod go mod tidy -go=1.20`
