// Demonstrates "run first, check later": this file has a real type error
// (string assigned to a number-typed variable below), yet the rule loads
// and fires instantly. The background tsgo check reports the error to the
// wb-rules log as a warning ("TS check: ...") without ever delaying rules.

defineVirtualDevice('ts_check_demo', {
  title: 'TS Checker Demo',
  cells: {
    poke: { type: 'switch', value: false },
    fired: { type: 'value', value: 0, readonly: true },
  },
});

let fireCount: number = 0;

defineRule('ts_check_demo_rule', {
  whenChanged: 'ts_check_demo/poke',
  then: () => {
    fireCount = 'not a number'; // <- deliberate TS error, still runs as JS
    dev['ts_check_demo/fired'] = (Number(fireCount) || 0) + 1;
    log.info('ts_check_demo fired, value = {}', fireCount);
  },
});
