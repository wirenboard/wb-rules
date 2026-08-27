#ifndef QJD_SHIM_H
#define QJD_SHIM_H

#include "quickjs.h"

/* Class IDs (process-wide, registered per-runtime). */
void qjd_install_rejection_tracker(JSRuntime *rt);
void qjd_run_gc(JSRuntime *rt, int anchor);
int64_t qjd_memory_used(JSRuntime *rt);
void qjd_set_memory_limit(JSRuntime *rt, int64_t limit); /* limit < 0 => unlimited */
extern JSClassID qjd_goobj_class_id;   /* wraps a Go registry id (opaque) */
extern JSClassID qjd_thread_class_id;  /* wraps a JSContext* (realm handle) */

void qjd_init_class_ids(void);
void qjd_register_classes(JSRuntime *rt);

/* JS_UNDEFINED etc. are struct-initializer macros — unusable from Go. */
/* Stack-anchor-safe wrappers: Go goroutines migrate OS threads between
 * cgo calls, so JS_UpdateStackTop and the deep-recursion operation must
 * happen inside ONE cgo call or QuickJS's stack-overflow heuristic
 * misfires ("SyntaxError: stack overflow" on perfectly fine code). */
JSValue qjd_eval(JSContext *ctx, const char *input, size_t len, const char *filename, int flags, int anchor);
JSValue qjd_call(JSContext *ctx, JSValue fn, JSValue this_val, int argc, JSValue *argv, int anchor);
JSValue qjd_call_ctor(JSContext *ctx, JSValue fn, int argc, JSValue *argv, int anchor);
JSValue qjd_eval_function(JSContext *ctx, JSValue fn, int anchor);
int qjd_execute_pending_job(JSRuntime *rt, JSContext **pctx, int anchor);
JSContext *qjd_new_context(JSRuntime *rt, int anchor);
JSValue qjd_invoke(JSContext *ctx, JSValue obj, JSAtom atom, int argc, JSValue *argv, int anchor);
JSValue qjd_json_stringify(JSContext *ctx, JSValue v, int anchor);
JSValue qjd_json_parse(JSContext *ctx, const char *buf, size_t len, const char *filename, int anchor);
JSValue qjd_new_obj_with_opaque_id(JSContext *ctx, JSClassID cid, uint64_t id);
JSValue qjd_undefined(void);
JSValue qjd_null(void);
JSValue qjd_true(void);
JSValue qjd_false(void);
JSValue qjd_exception(void);
int qjd_tag(JSValue v);

/* Promise state of v: JS_PROMISE_PENDING/FULFILLED/REJECTED, or -1 if v is
 * not a promise. qjd_promise_result returns a dup'd copy of the settled
 * value/reason. */
int qjd_promise_state(JSContext *ctx, JSValue v);
JSValue qjd_promise_result(JSContext *ctx, JSValue v);
void *qjd_value_ptr(JSValue v);

/* Push a Go function: a JS function whose func_data[0] is a goobj holder
 * carrying the Go registry id of the GoFunc. */
JSValue qjd_new_go_func(JSContext *ctx, uint64_t id);

/* Install the shim's require() C function into the context's global object. */
int qjd_install_require(JSContext *ctx);
JSValue qjd_new_require(JSContext *ctx, const char *base_id);

JSValue qjd_get_module_exports(JSContext *ctx, JSValue module);

/* Non-variadic wrappers (cgo cannot call variadic C functions). */
JSValue qjd_throw_type_error(JSContext *ctx, const char *msg);
JSValue qjd_throw_error(JSContext *ctx, const char *msg);
JSValue qjd_throw_range_error(JSContext *ctx, const char *msg);

/* "file:line:col" of the innermost bytecode frame at the time of the call;
 * defined in qjs_build.c (needs interpreter internals). See the watchdog
 * (goInterrupt) for why it is read from inside the interrupt handler. */
int qjd_top_bytecode_location(JSRuntime *rt, char *out, int out_size);

/* Helpers for opaque access without JSValue->ptr casting on the Go side. */
void *qjd_get_opaque(JSValue v, JSClassID cid);
JSValue qjd_new_obj_with_opaque(JSContext *ctx, JSClassID cid, void *p);

/* ES modules. The runtime-wide loader delegates to Go (goModuleNormalize /
 * goModuleLoad in duktape.go), which asks the engine's ModuleHost. */
void qjd_install_module_loader(JSRuntime *rt);
char *qjd_js_strdup(JSContext *ctx, const char *s);
/* Compile (but do not run) module source; the returned value has tag
 * JS_TAG_MODULE. Resolving the module's static imports happens here, so the
 * loader is invoked for every dependency during this call. */
JSValue qjd_compile_module(JSContext *ctx, const char *input, size_t len, const char *filename, int anchor);
/* Link and evaluate a compiled module; returns its evaluation promise. Safe
 * to call again on an already evaluated module (returns the same promise). */
JSValue qjd_eval_module(JSContext *ctx, JSValue modval, int anchor);
int qjd_is_module(JSValue v);
JSValue qjd_module_namespace(JSContext *ctx, JSValue modval);
JSValue qjd_import_meta(JSContext *ctx, JSValue modval);
JSModuleDef *qjd_module_def(JSValue modval);
JSValue qjd_module_value(JSContext *ctx, JSModuleDef *m);
/* A synthetic module wrapping an evaluated CommonJS module: `default` is
 * module.exports, every own enumerable string-keyed property of it is a named
 * export (a snapshot taken when the importer links, like Node.js). */
JSModuleDef *qjd_new_cjs_module(JSContext *ctx, const char *name, JSValue exports);

#endif
