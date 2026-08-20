/* global defineVirtualDevice, defineRule, dev, log */
// Engine benchmark rules — strict ES5 so the same file runs on Duktape and QuickJS.
defineVirtualDevice("bench", {
  title: "Engine Bench",
  cells: {
    in: { type: "value", value: 0, readonly: false },
    out: { type: "value", value: 0, readonly: true },
    compute: { type: "pushbutton" },
    compute_ms: { type: "text", value: "-", readonly: true }
  }
});
defineRule("bench_echo", {
  whenChanged: "bench/in",
  then: function (v) { dev.bench.out = v; }
});
defineRule("bench_compute", {
  whenChanged: "bench/compute",
  then: function () {
    var t0 = Date.now();
    var s = 0;
    for (var i = 0; i < 2000000; i++) s += i % 7;
    function fib(n) { return n < 2 ? n : fib(n - 1) + fib(n - 2); }
    var fr = fib(23);
    var dt = Date.now() - t0;
    dev.bench.compute_ms = "" + dt;
    log("bench compute: {} ms (s={}, fib23={})", dt, s, fr);
  }
});
