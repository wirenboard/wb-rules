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

/* qjd_top_bytecode_location writes "file:line:col" of the innermost bytecode
 * frame as the interpreter has recorded it so far - its latest call site.
 * It serves the watchdog: read from inside the interrupt handler, i.e.
 * before JS_CallInternal's exception path overwrites the frame's pc with
 * the jump target of the back-edge it was polling on (OP_goto moves pc
 * first and polls second), which build_backtrace then maps to the
 * statement BEFORE the loop. C-function frames (a builtin the script is
 * stalled in) are skipped: the position is the call into them. Needs
 * quickjs.c internals, hence this file. Returns 0 when no bytecode frame
 * with debug info is running or the buffer is too small. */
int qjd_top_bytecode_location(JSRuntime *rt, char *out, int out_size)
{
    JSStackFrame *sf;
    for (sf = rt->current_stack_frame; sf; sf = sf->prev_frame) {
        JSObject *p;
        JSFunctionBytecode *b;
        JSContext *ctx;
        const char *filename;
        int line, col, n;
        if (JS_VALUE_GET_TAG(sf->cur_func) != JS_TAG_OBJECT)
            continue;
        p = JS_VALUE_GET_OBJ(sf->cur_func);
        if (!js_class_has_bytecode(p->class_id))
            continue;
        b = p->u.func.function_bytecode;
        if (!b->has_debug || !sf->cur_pc)
            return 0;
        ctx = b->realm;
        line = find_line_num(ctx, b, sf->cur_pc - b->byte_code_buf - 1, &col);
        if (line == 0)
            return 0;
        filename = JS_AtomToCString(ctx, b->debug.filename);
        n = snprintf(out, out_size, "%s:%d:%d", filename ? filename : "<null>", line, col);
        JS_FreeCString(ctx, filename);
        return n > 0 && n < out_size;
    }
    return 0;
}
