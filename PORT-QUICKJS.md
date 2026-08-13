# wb-rules on QuickJS

This tree is wb-rules with the Duktape 1.0.2 engine replaced by **QuickJS
2026-06-04** (Bellard's latest release). The engine is pinned as a git
submodule at `third_party/quickjs` → bellard/quickjs commit `3d5e064`
(verified byte-identical to the official release tarball). The port keeps
the wbrules engine code intact by providing a QuickJS-backed drop-in for
the go-duktape API.

## Engine shipping model

- `third_party/quickjs` — submodule, unmodified upstream. If fixes are ever
  needed, fork bellard/quickjs into the wirenboard org, point `.gitmodules`
  there, and carry patches as commits — they rebase cleanly on upstream.
- `internal/quickjsduk/qjs_*.c` — five one-line compilation wrappers that
  `#include` the submodule sources. cgo only auto-compiles `.c` files inside
  the package directory, so these wrappers are the entire engine build:
  no Makefile step, no system library, and cross-compilation (armhf/arm64)
  rides the existing `CGO_ENABLED=1 CC=<cross-gcc>` flow unchanged. The
  wrappers also carry `#pragma GCC optimize("wrapv")` (upstream builds with
  -fwrapv, which cgo's flag filter rejects) and `CONFIG_VERSION` (update it
  together with the submodule).
- Clone with `git clone --recursive`, or `git submodule update --init`.

## Debian package

`dpkg-buildpackage -b` works with two packaging edits (committed): system
`golang-go` instead of the internal `golang-1.26-go`, and the matching PATH
in debian/rules. Build wbgo.so from wbgo-private **with the same flags the
deb uses** (`-trimpath -ldflags "-s -w"`) so the plugin/binary pair inside
the package match, and pass `WBGO_LOCAL_PATH`:

```sh
cd wbgo-private && go build -trimpath -buildvcs=false -ldflags "-s -w" \
    -buildmode=plugin -o amd64.wbgo.so .
cd ../wb-rules && WBGO_LOCAL_PATH=../wbgo-private dpkg-buildpackage -b -us -uc
```

Built and smoke-tested here: wb-rules_2.47.0~quickjs1_amd64.deb installs,
the service unit registers, and the engine runs rules end-to-end against a
real mosquitto broker. Note the flag coupling both ways: test binaries are
NOT built with -trimpath, so tests need a non-trimpath wbgo.so — the deb
build overwrites wbrules/wbgo.so with the trimpath one (rebuild it plain
before running go test again).

## What changed

1. **`internal/quickjsduk/`** — a Go package with module path
   `github.com/wirenboard/go-duktape`, wired in via `go.mod`:
   `replace github.com/wirenboard/go-duktape => ./internal/quickjsduk`.
   It reimplements the 70-method go-duktape surface wbrules uses on QuickJS
   (libquickjs.a built from source, cgo):
   - Duktape's value-stack semantics, incl. fresh stack frames for Go-func
     calls, `PushThis`, and negative-rc throws with Duktape's exact error
     strings (`Error: error error (rc -100)` — tests assert them);
   - `PushThreadNewGlobalenv` → `JS_NewContext` realm in the shared runtime
     (the per-script-file isolation mechanism), realm handle GC frees the realm;
   - heap stash, enumerator protocol, JSON codec, Go object wrappers
     (custom-class objects get `Object.prototype` — QuickJS default is null);
   - Duktape 1.x CommonJS: global `require()` + `Duktape.modSearch`, per-realm
     module cache, relative-id resolution, cycle-safe pre-registration,
     `module.filename`/`module.static` support;
   - `JS_UpdateStackTop` on every API entry — Go schedules goroutines across
     OS threads and QuickJS's stack-overflow heuristic is anchored to the
     creating thread's stack otherwise (symptom: spurious
     "Maximum call stack size exceeded" from any nested JS call).

2. **`wbrules/escontext.go`** — two engine-format adaptations:
   - `fileRx` parses QuickJS stack lines (`at fn (file:line:col)` and the
     syntax-error `at file:line:col` form) instead of Duktape's;
   - `GetESError` takes the message from the error value itself (Duktape's
     `.stack` embeds `"Error: msg"` as its first line; QuickJS's holds only
     frame lines).

3. **Everything else in `wbrules/` is untouched.** lib.js runs as-is
   (ES6 Proxy support in QuickJS covers what the Duktape fork provided).

## Test infrastructure

`wbgo.so` is built from wirenboard/wbgo-private:

```sh
cd wbgo-private && go build -buildvcs=false -buildmode=plugin -o amd64.wbgo.so .
cp amd64.wbgo.so ../wb-rules/wbrules/wbgo.so
```

Do NOT build the plugin with -trimpath unless the test binary uses it too —
Go plugin loading requires identical build IDs for shared packages.

## Test status (2026-08-12, real production wbgo.so from wbgo-private)

**All 36 test suites pass** against the production driver plugin, built from
wirenboard/wbgo-private with matching toolchain and dependency versions
(build the plugin WITHOUT -trimpath so shared-package build IDs match the
test binary; go build -buildvcs=false -buildmode=plugin).

Three test-data updates were needed, each an engine-behavior difference
documented below: rule location line attribution (24→17), StorableObject
for-in fields, and one log expectation in the email suite that had asserted
Duktape's CESU-8 surrogate leak — QuickJS logs the emoji as proper UTF-8
(the transmitted MIME message was already byte-identical).

## Engine semantics ported (hard-won details)

- **Calling-realm dispatch**: QuickJS invokes C functions in the function's
  *creation* realm; Duktape uses the calling thread's context. wb-rules keys
  per-file state (rule registries) on that context, so the shim tracks the
  actively-executing realm and dispatches Go callbacks against it.
- **Module semantics**: `require()` caches per realm (per script file) — a
  module shared by two rule files initializes twice, as wb-rules expects;
  relative ids resolve against the requiring module's id; require-cycles get
  the partially-built exports (pre-registration).
- **Error text parity**: negative-rc Go-function errors produce Duktape's
  exact strings ("Error: error error (rc -100)"); ESError messages embed the
  stack the way Duktape's `.stack` did (tests regexp-match file:line in them).
- **Two documented test-data changes**: rule locations attribute a multi-line
  `defineRule(...)` call to its FIRST line (QuickJS) instead of its last
  (Duktape) — `rule_location_test.go` expectations updated 24→17; and
  `scripts/lib.js` StorableObject bookkeeping fields are now non-enumerable
  (Duktape's legacy `enumerate` Proxy trap hid them; spec-correct for-in
  walks the proxy prototype chain).

## Hardware validation (WB8, arm64, 2026-08-13)

Deployed to a Wiren Board 8 (trixie, 4 GB) over the stock 2.46.2 install;
all production rule files load with zero script errors. Measured back-to-back
on the same device, same ruleset (steady state, 90 s after restart):

| metric | Duktape 2.46.2 | QuickJS 2.47.0~quickjs1 |
|---|---|---|
| RSS | 37.6 MB | 36.7 MB |
| PSS | 36.1 MB | 35.3 MB |
| MQTT reaction latency, median (n=300) | 6.98 ms | 7.61 ms |
| latency p99 | 12.3 ms | 14.7 ms |
| ES5 compute benchmark (2M-iter loop + fib(23)) | ~1300 ms | **~310 ms (4.2x faster)** |

Reaction latency is dominated by the MQTT/driver path — engine choice is
noise there. Raw compute is ~4x faster on QuickJS; memory is at parity.

## Pending-job pump (promises)

QuickJS queues promise reactions as pending jobs; Duktape 1.x had no
promises, so wb-rules never ran a microtask queue. The shim drains
`JS_ExecutePendingJob` whenever control returns to Go from the outermost JS
entry — async/await and promise chains in rules resolve as they would on an
event-loop runtime. (Found on-device: `Promise.withResolvers` never settled
until this was added.)

## Samples

- `sample-es2025.js` — ES2024/ES2025 feature showcase (class private
  fields/static blocks, toSorted/findLast/at, Object.groupBy, iterator
  helpers, Set algebra, Promise.withResolvers, RegExp.escape, v-flag
  regexps, BigInt, arrow-function rule callbacks). Deployable as-is.
- `sample-bench.js` — the ES5 benchmark rules used for the numbers above
  (runs on both engines).
