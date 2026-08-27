// On-controller registry typing for a plain .js file: with --checkJs the
// background check type-checks .js too, so wrong-typed writes to live controls
// (tsdev/count numeric, tsdev/trigger switch, defined by testrules_ts.ts) are
// flagged exactly like in .ts. References to devices that do not exist stay
// loose. Never executed (the body is a function that is never called).

function __registryCheck() {
  dev["tsdev/count"] = "not a number"; // line 8: registered numeric <- string, error
  getControl("tsdev/trigger").setValue(123); // line 9: registered switch <- number, error
  dev["tsdev/count"] = 42; // fine
  dev["unregistered/control"] = "anything"; // not in the registry -> loose, fine
}
