// ES2024/ES2025 feature showcase for the QuickJS-powered wb-rules engine.
// Everything below is impossible on the old Duktape 1.x (ES5) engine.
// Safe to run on a live controller: touches only its own virtual device.

// ES2022 class with private fields, private methods and a static init block
class TempStats {
  #samples = [];
  #max;
  static #limit;
  static {
    TempStats.#limit = 32;
  }

  add(value) {
    this.#samples.push(value);
    if (this.#samples.length > TempStats.#limit) this.#samples.shift();
    this.#max = Math.max(this.#max ?? -Infinity, value); // nullish coalescing
    return this;
  }

  #sorted() {
    return this.#samples.toSorted((a, b) => a - b); // ES2023, non-mutating
  }

  get median() {
    const s = this.#sorted();
    if (s.length === 0) return null;
    const mid = s.length >> 1;
    return s.length % 2 ? s.at(mid) : (s.at(mid - 1) + s.at(mid)) / 2; // .at()
  }

  get lastAboveMedian() {
    const m = this.median;
    return this.#samples.findLast((v) => v > m) ?? null; // ES2023 findLast
  }

  get summary() {
    // optional chaining + logical assignment + template literals
    const stats = { count: this.#samples.length, median: this.median };
    stats.max ??= this.#max;
    return `n=${stats.count} median=${stats.median?.toFixed?.(2) ?? "-"} max=${stats.max ?? "-"}`;
  }

  get buckets() {
    // ES2024 Object.groupBy + ES2025 iterator helpers (lazy pipeline)
    const m = this.median;
    const g = Object.groupBy(this.#samples, (v) => (v >= m ? "high" : "low"));
    return Object.entries(g)
      .values()
      .map(([k, arr]) => `${k}:${arr.length}`)
      .toArray()
      .join(" ");
  }
}

const stats = new TempStats();
let ticks = 0n; // BigInt tick counter

defineVirtualDevice("es2025_demo", {
  title: "ES2024/25 Demo (QuickJS)",
  cells: {
    median: { type: "value", value: 0, readonly: true, order: 1 },
    summary: { type: "text", value: "-", readonly: true, order: 2 },
    parsed: { type: "text", value: "-", readonly: true, order: 3 },
    async_check: { type: "text", value: "-", readonly: true, order: 4 },
    ticks: { type: "text", value: "0", readonly: true, order: 5 },
    poke: { type: "pushbutton", order: 6 },
  },
});

// Periodic sampling: every tick feeds the stats and updates the cells.
defineRule("es2025_sampler", {
  when: cron("@every 15s"),
  // arrow functions work as rule callbacks (note: `this` is lexical in
  // arrows, so use closures — as here — rather than `this` for state)
  then: () => {
    ticks += 1n;
    stats.add(20 + Math.random() * 10);
    dev.es2025_demo.median = stats.median ?? 0;
    dev.es2025_demo.summary = `${stats.summary} [${stats.buckets}]`;
    dev.es2025_demo.ticks = ticks.toString();
  },
});

// Pushbutton: exercise regexp named groups, replaceAll and async/await.
defineRule("es2025_poke", {
  whenChanged: "es2025_demo/poke",
  then: () => {
    // ES2018 named groups + ES2025 RegExp.escape + ES2024 v-flag class
    const raw = "temp=23.5;unit=C";
    const sep = RegExp.escape(";");
    const m = raw.match(new RegExp(`temp=(?<val>[\\d.]+)${sep}unit=(?<unit>[\\p{L}])`, "v"));
    dev.es2025_demo.parsed = `${m?.groups?.val ?? "?"}°${m?.groups?.unit ?? "?"}`.replaceAll("?", "-");

    // ES2024 Promise.withResolvers + ES2025 Set algebra, awaited in a rule
    (async () => {
      const { promise, resolve } = Promise.withResolvers();
      const wanted = new Set(["ok", "warn"]);
      const seen = new Set(["ok", "extra"]);
      resolve(wanted.intersection(seen).union(seen.difference(wanted)));
      const verdict = [...(await promise)].join("+") +
        (raw.isWellFormed() ? " wf" : " !wf");
      dev.es2025_demo.async_check = verdict;
      log("es2025 async verdict: {}", verdict);
    })();

    log("es2025 poke: {} (findLast>median: {})", stats.summary, stats.lastAboveMedian);
  },
});

log("es2025 demo loaded: groupBy={} withResolvers={} setUnion={}", typeof Object.groupBy, typeof Promise.withResolvers, typeof new Set().union);
