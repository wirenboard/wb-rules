// a TypeScript ES module by extension: no import/export needed to be one
export {};
globalThis.mtsLoaded = import.meta.filename.endsWith("/typed-only.mts");
log("Module esm typed-only.mts init");
