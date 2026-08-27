// A file that throws at the top level with no await: the async wrapper must
// still surface this as a synchronous load error (its promise is rejected
// before any await), not as a deferred async error.
throw new Error("tla load boom");
