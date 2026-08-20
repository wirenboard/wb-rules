// A legacy octal literal is valid sloppy JavaScript, but it is a SYNTAX
// error for TypeScript - and TypeScript reports no semantic diagnostics at
// all for a program with a syntax error. So this file gets exactly the octal
// diagnostic (which tells the user why the file is otherwise unchecked), and
// files checked in the same batch must still get their own type errors.

var mode = 0755; // line 7: TS1121, reported (as a warning); 0o755 fixes it
function __check() {
  dev["tsdev/count"] = "hidden by the syntax error above"; // line 9: not reported (TypeScript skips semantics)
}
