// importing a legacy sloppy .js module must not turn its own problems into
// diagnostics of this file; misuse of it here still is (line 6)
import { f } from "test/esm/sloppy";

log(f(1));
log(f("x", 2, 3)); // Expected 1 arguments, but got 3
export {};
