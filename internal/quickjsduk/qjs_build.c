/* Compiles the pinned, unmodified QuickJS from ../../third_party/quickjs
 * (git submodule). cgo only auto-compiles .c files in this directory, so
 * these wrappers are the entire build system for the engine:
 *  - the -I flag in duktape.go points at the submodule;
 *  - the wrapv pragma replaces upstream's -fwrapv (cgo's flag filter
 *    rejects it). GCC-only: clang ignores the pragma, so a clang build
 *    (macOS dev machines) runs QuickJS without wrapping signed overflow -
 *    fine for development, but release builds must use gcc (CI does);
 *  - CONFIG_VERSION must match third_party/quickjs/VERSION — update both
 *    when bumping the submodule. */
#pragma GCC optimize ("wrapv")
#define CONFIG_VERSION "2026-06-04"
#include "quickjs.c"
