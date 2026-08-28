// TypeScript ES module in the module directories
export interface Point {
  x: number;
  y: number;
}

export function typedAdd(a: number, b: number): number {
  return a + b;
}

export function explode(where: string): never {
  // line 13: the traceback must point here, in the .ts source
  throw new Error("typed module boom in " + where);
}

log("Module esm typed init");
