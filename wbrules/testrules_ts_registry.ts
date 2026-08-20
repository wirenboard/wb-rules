// On-controller registry typing: tsdev/count is numeric and tsdev/trigger is
// a switch on this controller (defined by testrules_ts.ts, loaded first), so
// the background check - which now generates a WbControls registry from the
// live device table - must flag wrong-typed writes to them, exactly like the
// editor. References to devices that do not exist stay loose. Never executed
// (the body is a function that is never called).
function __registryCheck() {
  dev["tsdev/count"] = "not a number"; // line 8: registered numeric <- string, error
  getControl("tsdev/trigger").setValue(123); // line 9: registered switch <- number, error
  dev["tsdev/count"] = 42; // fine
  dev["unregistered/control"] = "anything"; // not in the registry -> loose, fine
}
