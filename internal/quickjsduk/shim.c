#include "shim.h"
#include <string.h>

JSClassID qjd_goobj_class_id;
JSClassID qjd_thread_class_id;

/* Implemented in Go (duktape.go). */
extern int goInterrupt(JSRuntime *rt);
extern JSValue goFuncCall(JSContext *ctx, JSValue this_val, int argc,
                          JSValue *argv, uint64_t id);
extern void goObjFinalize(uint64_t id);
extern void goThreadFinalize(void *jsctx);
extern void goPromiseRejection(JSRuntime *rt, void *promise, const char *msg,
                               const char *stack, int is_handled);
extern JSValue goRequire(JSContext *ctx, JSValue this_val, int argc, JSValue *argv,
                         JSValue baseId);

JSValue qjd_undefined(void) { return JS_UNDEFINED; }
JSValue qjd_null(void) { return JS_NULL; }
JSValue qjd_true(void) { return JS_TRUE; }
JSValue qjd_false(void) { return JS_FALSE; }
JSValue qjd_exception(void) { return JS_EXCEPTION; }
// NORM_TAG, not GET_TAG: under JS_NAN_BOXING (32-bit builds) a float64 has
// no JS_TAG_FLOAT64 in the raw tag - the normalized form is the only
// representation-independent one. With the raw tag every fractional number
// on armhf classified as an object.
int qjd_tag(JSValue v) { return JS_VALUE_GET_NORM_TAG(v); }

/* JS_PromiseState returns -1 for a non-promise (its JS_GetOpaque yields
 * NULL), so this is safe to call on any value. */
int qjd_promise_state(JSContext *ctx, JSValue v) { return (int)JS_PromiseState(ctx, v); }
JSValue qjd_promise_result(JSContext *ctx, JSValue v) { return JS_PromiseResult(ctx, v); }

/* Identity pointer of a value, matching the key the rejection tracker uses
 * (JS_VALUE_GET_PTR of the promise). Lets Go retract a pending rejection it
 * has decided to surface synchronously. */
void *qjd_value_ptr(JSValue v) { return JS_VALUE_GET_PTR(v); }

static void goobj_finalizer(JSRuntime *rt, JSValue val) {
    void *p = JS_GetOpaque(val, qjd_goobj_class_id);
    if (p)
        goObjFinalize((uint64_t)(uintptr_t)p);
}

static void thread_finalizer(JSRuntime *rt, JSValue val) {
    void *p = JS_GetOpaque(val, qjd_thread_class_id);
    if (p)
        goThreadFinalize(p);
}

static JSClassDef goobj_class = { "GoObject", .finalizer = goobj_finalizer };
static JSClassDef thread_class = { "DukThread", .finalizer = thread_finalizer };

void qjd_init_class_ids(void) {
    /* idempotent per quickjs: assigns on first call only */
    JS_NewClassID(&qjd_goobj_class_id);
    JS_NewClassID(&qjd_thread_class_id);
}

static int interrupt_trampoline(JSRuntime *rt, void *opaque) {
    return goInterrupt(rt);
}

void qjd_register_classes(JSRuntime *rt) {
    JS_NewClass(rt, qjd_goobj_class_id, &goobj_class);
    JS_NewClass(rt, qjd_thread_class_id, &thread_class);
    JS_SetInterruptHandler(rt, interrupt_trampoline, NULL);
}

static JSValue go_func_trampoline(JSContext *ctx, JSValueConst this_val,
                                  int argc, JSValueConst *argv, int magic,
                                  JSValue *func_data) {
    void *p = JS_GetOpaque(func_data[0], qjd_goobj_class_id);
    return goFuncCall(ctx, this_val, argc, (JSValue *)argv,
                      (uint64_t)(uintptr_t)p);
}

JSValue qjd_new_go_func(JSContext *ctx, uint64_t id) {
    JSValue holder = JS_NewObjectClass(ctx, qjd_goobj_class_id);
    JS_SetOpaque(holder, (void *)(uintptr_t)id);
    JSValue fn = JS_NewCFunctionData(ctx, go_func_trampoline, 0, 0, 1, &holder);
    JS_FreeValue(ctx, holder); /* func_data holds its own reference */
    return fn;
}

static JSValue require_trampoline(JSContext *ctx, JSValueConst this_val,
                                  int argc, JSValueConst *argv, int magic,
                                  JSValue *func_data) {
    return goRequire(ctx, this_val, argc, (JSValue *)argv, func_data[0]);
}

/* A require() bound to the id of the module that received it, so relative
 * ids ("./x") resolve Duktape-style against the requiring module. */
JSValue qjd_new_require(JSContext *ctx, const char *base_id) {
    JSValue base = JS_NewString(ctx, base_id);
    JSValue fn = JS_NewCFunctionData(ctx, require_trampoline, 1, 0, 1, &base);
    JS_FreeValue(ctx, base);
    return fn;
}

int qjd_install_require(JSContext *ctx) {
    JSValue glob = JS_GetGlobalObject(ctx);
    JSValue fn = qjd_new_require(ctx, "");
    int r = JS_SetPropertyStr(ctx, glob, "require", fn);
    /* Duktape exposes a global 'Duktape' object; wb-rules assigns
     * Duktape.modSearch on it per context. Recreate the pieces user
     * scripts rely on: a NUMERIC version and the enc/dec codecs.
     *
     * The codecs are a documented approximation, not a byte-exact port:
     * Duktape's dec returned a plain buffer whose string coercion read the
     * bytes as the string's internal UTF-8 form. Here dec returns a string
     * decoded from UTF-8 directly - identical for text payloads (the
     * observed rule usage) - so byte indexing of the result (buffer[i] as a
     * number) and byte-exact round-trips of non-UTF-8 binary data are NOT
     * preserved; enc coerces non-string input via String(). jx/jc are plain
     * JSON, and base64 decoding is lenient. No single QuickJS value
     * reproduces buffer semantics both as bytes and in string contexts;
     * the string form serves the corpus-observed uses. See the behaviour
     * differences entry in debian/changelog and docs/arc42 8.6. */
    JSValue duk = JS_NewObject(ctx);
    JS_SetPropertyStr(ctx, duk, "version", JS_NewInt32(ctx, 10002));
    r |= JS_SetPropertyStr(ctx, glob, "Duktape", duk);
    JS_FreeValue(ctx, glob);
    static const char codecs[] =
        "(function(){var B64='ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';\n"
        "function u8(s){if(typeof s!=='string'){s=String(s);}var out=[];for(var i=0;i<s.length;i++){var c=s.codePointAt(i);"
        "if(c>0xFFFF)i++;if(c<0x80)out.push(c);else if(c<0x800){out.push(0xC0|c>>6,0x80|c&63);}"
        "else if(c<0x10000){out.push(0xE0|c>>12,0x80|c>>6&63,0x80|c&63);}else{out.push(0xF0|c>>18,0x80|c>>12&63,0x80|c>>6&63,0x80|c&63);}}return out;}\n"
        "function fromU8(a){var s='',i=0;while(i<a.length){var b=a[i++];var c;"
        "if(b<0x80)c=b;else if(b<0xE0){c=(b&31)<<6|a[i++]&63;}else if(b<0xF0){c=(b&15)<<12|(a[i++]&63)<<6|a[i++]&63;}"
        "else{c=(b&7)<<18|(a[i++]&63)<<12|(a[i++]&63)<<6|a[i++]&63;}s+=String.fromCodePoint(c);}return s;}\n"
        "Duktape.enc=function(fmt,data){var a=u8(data);\n"
        " if(fmt==='hex'){return a.map(function(b){return(b<16?'0':'')+b.toString(16);}).join('');}\n"
        " if(fmt==='base64'){var s='';for(var i=0;i<a.length;i+=3){var n=(a[i]<<16)|((a[i+1]||0)<<8)|(a[i+2]||0);"
        "s+=B64[n>>18]+B64[n>>12&63]+(i+1<a.length?B64[n>>6&63]:'=')+(i+2<a.length?B64[n&63]:'=');}return s;}\n"
        " if(fmt==='jx'||fmt==='jc'){return JSON.stringify(data);}\n"
        " throw new Error('Duktape.enc: unsupported format '+fmt);};\n"
        "Duktape.dec=function(fmt,data){\n"
        " if(fmt==='hex'){var a=[];for(var i=0;i<data.length;i+=2){a.push(parseInt(data.substr(i,2),16));}return fromU8(a);}\n"
        " if(fmt==='base64'){var a=[],i=0;data=String(data).replace(/=+$/,'');"
        "for(;i+1<data.length;i+=4){var n=(B64.indexOf(data[i])<<18)|(B64.indexOf(data[i+1])<<12)|"
        "((B64.indexOf(data[i+2])&63)<<6)|(B64.indexOf(data[i+3])&63);a.push(n>>16&255);"
        "if(data[i+2]!==undefined)a.push(n>>8&255);if(data[i+3]!==undefined)a.push(n&255);}return fromU8(a);}\n"
        " if(fmt==='jx'||fmt==='jc'){return JSON.parse(data);}\n"
        " throw new Error('Duktape.dec: unsupported format '+fmt);};})();";
    JSValue polyfill = JS_Eval(ctx, codecs, sizeof(codecs) - 1, "duktape-codecs", 0);
    if (JS_IsException(polyfill)) {
        JS_FreeValue(ctx, JS_GetException(ctx));
        r = -1;
    }
    JS_FreeValue(ctx, polyfill);
    return r;
}

JSValue qjd_get_module_exports(JSContext *ctx, JSValue module) {
    return JS_GetPropertyStr(ctx, module, "exports");
}

JSValue qjd_throw_type_error(JSContext *ctx, const char *msg) {
    return JS_ThrowTypeError(ctx, "%s", msg);
}

JSValue qjd_throw_error(JSContext *ctx, const char *msg) {
    return JS_ThrowInternalError(ctx, "%s", msg);
}

JSValue qjd_throw_range_error(JSContext *ctx, const char *msg) {
    return JS_ThrowRangeError(ctx, "%s", msg);
}

void *qjd_get_opaque(JSValue v, JSClassID cid) { return JS_GetOpaque(v, cid); }

JSValue qjd_new_obj_with_opaque(JSContext *ctx, JSClassID cid, void *p) {
    JSValue o = JS_NewObjectClass(ctx, cid);
    /* Custom classes default to a null prototype in QuickJS; Duktape's
     * plain-object Go wrappers had Object.prototype. Restore it so
     * toString/property access on Go objects works. */
    JSValue glob = JS_GetGlobalObject(ctx);
    JSValue objctor = JS_GetPropertyStr(ctx, glob, "Object");
    JSValue objproto = JS_GetPropertyStr(ctx, objctor, "prototype");
    JS_SetPrototype(ctx, o, objproto);
    JS_FreeValue(ctx, objproto);
    JS_FreeValue(ctx, objctor);
    JS_FreeValue(ctx, glob);
    JS_SetOpaque(o, p);
    return o;
}

/* The JS-executing entry points take an `anchor` flag: when set, the C
 * stack top QuickJS measures its overflow check against is re-anchored to
 * the current thread - in the SAME cgo call as the execution, since Go may
 * move the goroutine to another OS thread between two calls. The caller
 * passes 1 only for an OUTERMOST entry (no Go->JS call in progress): a
 * nested entry - JS called a Go builtin which calls back into JS - runs on
 * the same, thread-locked OS thread BELOW the outer C frames, and
 * re-anchoring there would hand every nesting level a fresh budget and
 * turn a runaway JS<->Go recursion into a segfault instead of an
 * InternalError. */
JSValue qjd_eval(JSContext *ctx, const char *input, size_t len, const char *filename, int flags, int anchor) {
    if (anchor) JS_UpdateStackTop(JS_GetRuntime(ctx));
    return JS_Eval(ctx, input, len, filename, flags);
}

JSValue qjd_call(JSContext *ctx, JSValue fn, JSValue this_val, int argc, JSValue *argv, int anchor) {
    if (anchor) JS_UpdateStackTop(JS_GetRuntime(ctx));
    return JS_Call(ctx, fn, this_val, argc, argv);
}

JSValue qjd_call_ctor(JSContext *ctx, JSValue fn, int argc, JSValue *argv, int anchor) {
    if (anchor) JS_UpdateStackTop(JS_GetRuntime(ctx));
    return JS_CallConstructor(ctx, fn, argc, argv);
}

JSValue qjd_eval_function(JSContext *ctx, JSValue fn, int anchor) {
    if (anchor) JS_UpdateStackTop(JS_GetRuntime(ctx));
    return JS_EvalFunction(ctx, fn);
}

int qjd_execute_pending_job(JSRuntime *rt, JSContext **pctx, int anchor) {
    if (anchor) JS_UpdateStackTop(rt);
    return JS_ExecutePendingJob(rt, pctx);
}

JSContext *qjd_new_context(JSRuntime *rt, int anchor) {
    if (anchor) JS_UpdateStackTop(rt);
    return JS_NewContext(rt);
}

/* Fabricating a pointer from an integer id must happen in C: Go's
 * checkptr (enabled by -race) rejects unsafe.Pointer(uintptr(id)). */
JSValue qjd_new_obj_with_opaque_id(JSContext *ctx, JSClassID cid, uint64_t id) {
    return qjd_new_obj_with_opaque(ctx, cid, (void *)(uintptr_t)id);
}

JSValue qjd_invoke(JSContext *ctx, JSValue obj, JSAtom atom, int argc, JSValue *argv, int anchor) {
    if (anchor) JS_UpdateStackTop(JS_GetRuntime(ctx));
    return JS_Invoke(ctx, obj, atom, argc, argv);
}

JSValue qjd_json_stringify(JSContext *ctx, JSValue v, int anchor) {
    if (anchor) JS_UpdateStackTop(JS_GetRuntime(ctx));
    return JS_JSONStringify(ctx, v, JS_UNDEFINED, JS_UNDEFINED);
}

JSValue qjd_json_parse(JSContext *ctx, const char *buf, size_t len, const char *filename, int anchor) {
    if (anchor) JS_UpdateStackTop(JS_GetRuntime(ctx));
    return JS_ParseJSON(ctx, buf, len, filename);
}

/* Unhandled promise rejections (an async rule callback that throws after
 * an await rejects its implicit promise; nobody awaits it) never surface
 * through JS_ExecutePendingJob - the reaction job "succeeds" by rejecting
 * the derived promise. The host tracker is the only signal. The reason is
 * stringified immediately (it may die with its realm before the report);
 * the promise pointer is used purely as an identity key so a rejection
 * handled later in the same turn can retract the pending report. */
static void qjd_rejection_tracker(JSContext *ctx, JSValueConst promise,
                                  JSValueConst reason, JS_BOOL is_handled,
                                  void *opaque)
{
    (void)opaque;
    JSRuntime *rt = JS_GetRuntime(ctx);
    if (is_handled) {
        goPromiseRejection(rt, JS_VALUE_GET_PTR(promise), NULL, NULL, 1);
        return;
    }
    const char *msg = JS_ToCString(ctx, reason);
    if (!msg)
        JS_FreeValue(ctx, JS_GetException(ctx));
    const char *stack = NULL;
    JSValue stackv = JS_UNDEFINED;
    if (JS_IsObject(reason)) {
        stackv = JS_GetPropertyStr(ctx, reason, "stack");
        if (JS_IsString(stackv))
            stack = JS_ToCString(ctx, stackv);
        else if (JS_IsException(stackv))
            JS_FreeValue(ctx, JS_GetException(ctx));
    }
    goPromiseRejection(rt, JS_VALUE_GET_PTR(promise), msg, stack, 0);
    if (stack)
        JS_FreeCString(ctx, stack);
    JS_FreeValue(ctx, stackv);
    if (msg)
        JS_FreeCString(ctx, msg);
}

void qjd_install_rejection_tracker(JSRuntime *rt)
{
    JS_SetHostPromiseRejectionTracker(rt, qjd_rejection_tracker, NULL);
}

void qjd_run_gc(JSRuntime *rt, int anchor)
{
    if (anchor) JS_UpdateStackTop(rt);
    JS_RunGC(rt);
}

/* memory_used_size after a GC pass - the leak canary for tests */
int64_t qjd_memory_used(JSRuntime *rt)
{
    JSMemoryUsage mu;
    JS_ComputeMemoryUsage(rt, &mu);
    return mu.memory_used_size;
}

/* Cap total bytes the JS heap may allocate. A negative limit becomes
 * (size_t)-1, i.e. unlimited (the QuickJS default). When the cap is hit,
 * allocation fails and the running script throws out-of-memory instead of
 * the whole process growing until it is killed. */
void qjd_set_memory_limit(JSRuntime *rt, int64_t limit)
{
    JS_SetMemoryLimit(rt, (size_t)limit);
}
