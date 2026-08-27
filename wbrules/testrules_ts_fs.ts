// TypeScript rule file using the built-in fs module through every import
// style (each becomes a require() call in the CommonJS output the engine
// runs), plus an export - exports.* assignments land on the file's exports
// object. Used by RuleTsFsSuite; the error line below is asserted.
import * as fs from "fs";
import fsDefault from "fs";
import { readFileSync, existsSync } from "fs";
import * as fsp from "fs/promises";

export const marker = "ts-fs";

const self: string = readFileSync(__filename);
const stats: fs.Stats = fs.statSync(__filename);
const same = fsDefault.readFileSync(__filename) === self && fsp.readFile === fs.promises.readFile;
log("ts fs: {} {} {} {}", self.indexOf("ts-fs") > 0, stats.isFile(), same, existsSync(__filename + ".nope"));

const first: string = await fs.readFile(__filename).then((s) => s.split("\n")[0]);
log("ts fs async: {}", first.indexOf("TypeScript rule file") > 0);

defineRule("ts_fs_thrower", {
  whenChanged: "somedev/sw",
  then: () => {
    throw new Error("ts-fs-boom"); // line 23: asserted by TestTsFsErrorLineNumbers
  },
});

defineRule("ts_fs_async_thrower", {
  whenChanged: "somedev/temp",
  then: async () => {
    await fs.readFile(__filename);
    throw new Error("ts-fs-async-boom"); // line 31: asserted by TestTsFsAsyncErrorLineNumbers
  },
});
