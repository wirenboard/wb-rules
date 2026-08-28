// ES module with a relative import of a sibling module and a counter kept in
// import.meta.static (shared by every importing file, like module.static)
import { double } from "./util.js";

export function greet(name) {
  return "hello " + name + " " + double(1);
}

export function counter() {
  import.meta.static.n = (import.meta.static.n || 0) + 1;
  return import.meta.static.n;
}

export function whereAmI() {
  return [
    import.meta.filename.endsWith("/test/esm/helper.js"),
    import.meta.dirname.endsWith("/test/esm"),
    import.meta.url.startsWith("file:///"),
    // the realm global names the rule file, as it does for require()d modules
    __filename.indexOf("testrules_esm") >= 0,
  ].join(" ");
}

log("Module esm helper init");
