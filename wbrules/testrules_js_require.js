// A plain .js rule file that require()s wb-rules modules and uses the
// constructible forms of PersistentStorage / StorableObject, the way the
// shipped system rules do. With --checkJs TypeScript treats a call to the
// ambient `require` as a CommonJS import; the wildcard module declaration in
// wb-rules.d.ts must keep these loose instead of "Cannot find module".
// Never executed (the body is a function that is never called).

function __requireCheck() {
  var Logger = require('logger.mod').Logger; // line 9: must NOT be flagged
  var log = new Logger('check');
  var ps = new PersistentStorage('check-storage', { global: true }); // line 11: constructible
  ps.count = 1;
  ps.tracked = new StorableObject({ n: 1 }); // line 13: constructible
  log.info(ps.count);
  dev["tsdev/count"] = "not a number"; // line 15: still type-checked - registered numeric <- string
}
