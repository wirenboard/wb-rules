// TypeScript demo rule for wb-rules + QuickJS + tsgo.
// Loads as-is from /etc/wb-rules/ — transpiled on load, type-checked
// asynchronously (warnings go to the wb-rules log, execution never waits).

interface ClimateSample {
  temperature: number;
  humidity?: number;
  takenAt: string;
}

const HISTORY_LIMIT = 5;
const readings: ClimateSample[] = [];

defineVirtualDevice('ts_demo', {
  title: 'TypeScript Demo',
  cells: {
    temperature: { type: 'temperature', value: 0, readonly: false },
    average: { type: 'value', value: 0, readonly: true },
    samples: { type: 'value', value: 0, readonly: true },
    status: { type: 'text', value: 'no data', readonly: true },
  },
});

const average = (xs: number[]): number =>
  xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : 0;

defineRule('ts_demo_track', {
  whenChanged: 'ts_demo/temperature',
  then: (value) => {
    const sample: ClimateSample = {
      temperature: Number(value),
      takenAt: new Date().toISOString(),
    };
    readings.push(sample);
    if (readings.length > HISTORY_LIMIT) readings.shift();

    const avg = average(readings.map((s) => s.temperature));
    dev['ts_demo/average'] = Math.round(avg * 10) / 10;
    dev['ts_demo/samples'] = readings.length;

    // optional chaining + nullish coalescing + template literals
    const last = readings.at(-1)?.takenAt ?? 'never';
    dev['ts_demo/status'] = `avg ${avg.toFixed(1)} of ${readings.length}, last ${last}`;
    log.info('ts_demo: {} samples, avg {}', readings.length, avg.toFixed(1));
  },
});
