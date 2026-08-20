// Top-level await in a TypeScript rule file: with the check in module mode
// this must load and type-check with no "await needs a module" warning.
const answer: number = await Promise.resolve(42);
log.info("ts tla answer: {}", answer);
