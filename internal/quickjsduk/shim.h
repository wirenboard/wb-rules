#ifndef QJD_SHIM_H
#define QJD_SHIM_H

#include "quickjs.h"

/* Class IDs (process-wide, registered per-runtime). */
extern JSClassID qjd_goobj_class_id;   /* wraps a Go registry id (opaque) */
extern JSClassID qjd_thread_class_id;  /* wraps a JSContext* (realm handle) */

void qjd_init_class_ids(void);
void qjd_register_classes(JSRuntime *rt);

/* JS_UNDEFINED etc. are struct-initializer macros — unusable from Go. */
JSValue qjd_undefined(void);
JSValue qjd_null(void);
JSValue qjd_true(void);
JSValue qjd_false(void);
JSValue qjd_exception(void);
int qjd_tag(JSValue v);

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

/* Helpers for opaque access without JSValue->ptr casting on the Go side. */
void *qjd_get_opaque(JSValue v, JSClassID cid);
JSValue qjd_new_obj_with_opaque(JSContext *ctx, JSClassID cid, void *p);

#endif
