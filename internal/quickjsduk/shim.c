#include "shim.h"
#include <string.h>

JSClassID qjd_goobj_class_id;
JSClassID qjd_thread_class_id;

/* Implemented in Go (duktape.go). */
extern JSValue goFuncCall(JSContext *ctx, JSValue this_val, int argc,
                          JSValue *argv, uint64_t id);
extern void goObjFinalize(uint64_t id);
extern void goThreadFinalize(void *jsctx);
extern JSValue goRequire(JSContext *ctx, JSValue this_val, int argc, JSValue *argv,
                         JSValue baseId);

JSValue qjd_undefined(void) { return JS_UNDEFINED; }
JSValue qjd_null(void) { return JS_NULL; }
JSValue qjd_true(void) { return JS_TRUE; }
JSValue qjd_false(void) { return JS_FALSE; }
JSValue qjd_exception(void) { return JS_EXCEPTION; }
int qjd_tag(JSValue v) { return JS_VALUE_GET_TAG(v); }

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

void qjd_register_classes(JSRuntime *rt) {
    JS_NewClass(rt, qjd_goobj_class_id, &goobj_class);
    JS_NewClass(rt, qjd_thread_class_id, &thread_class);
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
     * Duktape.modSearch on it per context. Provide an empty one. */
    JSValue duk = JS_NewObject(ctx);
    JSValue ver = JS_NewString(ctx, "quickjs-shim");
    JS_SetPropertyStr(ctx, duk, "version", ver);
    r |= JS_SetPropertyStr(ctx, glob, "Duktape", duk);
    JS_FreeValue(ctx, glob);
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
