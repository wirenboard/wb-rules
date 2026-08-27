// ES module awaiting at the top level: import works (the importer waits),
// require() cannot (ERR_REQUIRE_ASYNC_MODULE)
export const v = await Promise.resolve(7);
log("Module esm tla init");
