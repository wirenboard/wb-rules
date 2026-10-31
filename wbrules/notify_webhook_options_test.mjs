// Run without a controller: node wbrules/notify_webhook_options_test.mjs
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
const __dirname = path.dirname(fileURLToPath(import.meta.url));
let request;
const log = Object.assign(() => { /* Intentional no-op: controller logging is stubbed in this test. */ }, {error: () => { /* Intentional no-op: controller logging is stubbed in this test. */ }});
const context = {exports: {}, log, debug: () => { /* Intentional no-op: controller logging is stubbed in this test. */ }, runShellCommand: (cmd, options) => {
  request = {cmd, options};
}};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../modules/wb-notify.js'), 'utf8'), context);
const send = context.exports.sendWebhook;
send({url: 'https://example.org', body: 'hello'}, function () {
  assert.equal(arguments.length, 1);
  assert.equal(arguments[0], null);
});
assert.equal(request.cmd, "curl -sS --fail -X 'POST' -H 'Content-Type: text/plain; charset=utf-8' --data-binary @- -- 'https://example.org'");
request.options.exitCallback(0, 'ok', '');
for (const name of ['connectTimeout', 'timeout']) {
  for (const value of [0, -1, NaN, Infinity, '2', null]) {
    assert.throws(() => send({url: 'https://example.org', [name]: value}));
  }
}
for (const value of ['relative', '/a\nb', '/a\rb', '/a\0b', null]) {
  assert.throws(() => send({url: 'https://example.org', headersFile: value}));
}
assert.throws(() => send({url: 'https://example.org', returnResponse: 1}));
let result;
send({url: 'https://example.org', connectTimeout: 1.5, timeout: 4,
  headersFile: "/tmp/header's", returnResponse: true}, (err, response) => {result = {err, response};});
assert.ok(request.cmd.includes('--connect-timeout 1.5 --max-time 4'));
assert.ok(request.cmd.includes("-H '@/tmp/header'\\''s'"));
assert.ok(request.cmd.includes('WB_NOTIFY_HTTP_STATUS:%{http_code}'));
request.options.exitCallback(0, 'body\nWB_NOTIFY_HTTP_STATUS:123\nWB_NOTIFY_HTTP_STATUS:201', '');
assert.equal(result.err, null);
assert.equal(result.response.statusCode, 201);
assert.equal(result.response.body, 'body\nWB_NOTIFY_HTTP_STATUS:123');
request.options.exitCallback(22, '\nWB_NOTIFY_HTTP_STATUS:403', 'secret');
assert.equal(result.response.statusCode, 403);
assert.equal(result.response.exitCode, 22);
assert.ok(!result.err.message.includes('secret'));
request.options.exitCallback(28, '\nWB_NOTIFY_HTTP_STATUS:000', '');
assert.equal(result.response.statusCode, null);
request.options.exitCallback(7, '', '');
assert.equal(result.response.statusCode, null);
console.log('Webhook options: legacy command/callback, validation, quoting, HTTP status and transport errors passed');

// Exercise the generated command with real curl against a loopback server only.
import http from 'node:http';
import os from 'node:os';
import {execFile} from 'node:child_process';
(async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'wb-webhook-'));
  const headerFile = path.join(dir, "header's");
  // The path is inside our freshly created private temp directory, never external input.
  // Exclusive creation prevents following an existing symlink; cleanup runs below.
  // eslint-disable-next-line security/detect-non-literal-fs-filename
  const handle = await fs.promises.open(headerFile, 'wx', 0o600);
  await handle.writeFile('X-Test: private-value\n');
  await handle.close();
  const server = http.createServer((req, res) => {
    assert.equal(req.headers['x-test'], 'private-value');
    if (req.url === '/slow') return setTimeout(() => res.end('late'), 500);
    res.writeHead(req.url === '/fail' ? 403 : 201);
    res.end('response body');
  });
  try {
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    context.runShellCommand = (cmd, options) => {
      // Execute the module-generated shell command, as wb-rules does; inputs here are test constants.
      const child = execFile('/bin/sh', ['-c', cmd], (err, stdout, stderr) => options.exitCallback(err ? err.code : 0, stdout, stderr));
      child.stdin.end(options.input);
    };
    const call = route => new Promise(resolve => send({
      url: 'http://127.0.0.1:' + server.address().port + route,
      headersFile: headerFile, returnResponse: true, connectTimeout: 1, timeout: 0.1,
    }, (err, response) => resolve({err, response})));
    let value = await call('/ok');
    assert.equal(value.err, null);
    assert.equal(value.response.statusCode, 201);
    assert.equal(value.response.body, 'response body');
    value = await call('/fail');
    assert.equal(value.response.statusCode, 403);
    assert.equal(value.response.exitCode, 22);
    value = await call('/slow');
    assert.equal(value.response.exitCode, 28);
    assert.equal(value.response.statusCode, null);
    console.log('Loopback curl: header file, success, HTTP failure and deadline passed');
  } finally {
    server.close();
    fs.rmSync(dir, {recursive: true, force: true});
  }
})().catch(err => {console.error(err); process.exitCode = 1;});
