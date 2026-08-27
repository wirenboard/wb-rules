// Twelve wrong-typed writes to a registered numeric control: the journal
// shows the first ten TS-check lines plus one summary line; Editor.Check
// still returns all twelve. Never executed.
function __manyErrors() {
  dev["tsdev/count"] = "e1"; // line 5
  dev["tsdev/count"] = "e2"; // line 6
  dev["tsdev/count"] = "e3"; // line 7
  dev["tsdev/count"] = "e4"; // line 8
  dev["tsdev/count"] = "e5"; // line 9
  dev["tsdev/count"] = "e6"; // line 10
  dev["tsdev/count"] = "e7"; // line 11
  dev["tsdev/count"] = "e8"; // line 12
  dev["tsdev/count"] = "e9"; // line 13
  dev["tsdev/count"] = "e10"; // line 14
  dev["tsdev/count"] = "e11"; // line 15
  dev["tsdev/count"] = "e12"; // line 16
}
