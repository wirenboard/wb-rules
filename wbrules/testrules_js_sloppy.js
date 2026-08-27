// Sloppy-mode JavaScript idioms that TypeScript rejects but that are valid
// and intended in a rule file: with checkJs they must NOT be reported for a
// .js file, while a real type error still is (as a warning - JavaScript is
// not typed by contract). Lines are referenced by the test.

var t0 = new Date();
var elapsed = new Date() - t0; // line 7: Date arithmetic (TS2362) - idiomatic JS, not reported
function __check() {
  if (elapsed > 1000) {
    dev["tsdev/count"] = "not a number"; // line 10: a real finding - reported (as a warning)
  }
}
