// Type-error-but-runtime-valid file: the rule must run immediately while
// the async check reports the type error later.
const wrong: number = "not a number" as unknown as string as any; // fools transpile
let alsoWrong: string;
alsoWrong = 42 as any;
const reallyWrong: boolean = "yes"; // line 6: plain type error for the checker

defineRule("ts_badtypes_rule", {
  whenChanged: "tsdev/trigger",
  then: () => {
    log("badtypes rule fired anyway: {} {}", wrong, alsoWrong);
  },
});
