// a library next to the rule files, reached by a relative import
import { double } from "test/esm/util";
export const sib = "sibling " + double(21);
log("Module esmlib sib init");
