// defineAlias creates a bare global at run time; the generated registry
// declares each alias, so documented alias usage is not "Cannot find name".
defineAlias("tsAliasLamp", "tsdev/trigger");
function __aliasCheck() {
  tsAliasLamp = true; // line 5: declared by the registry, no TS2304
  var v = tsAliasLamp; // line 6: readable too
  return v;
}
