// The built-in fs module, exercised at load time: the synchronous API
// before the first await, the promise API after it. The sandbox lives next
// to this file (the suite's temp dir), so paths are logged relative to it.
/* global log, __filename */
var fs = require('fs');
var base = __filename.slice(0, __filename.lastIndexOf('/'));
var dir = base + '/fs-sandbox';
function rel(p) {
  return p.slice(base.length);
}

fs.rmSync(dir, { recursive: true, force: true });
log('mkdir: ' + rel(fs.mkdirSync(dir + '/a/b', { recursive: true })));
log('mkdir again: ' + fs.mkdirSync(dir + '/a/b', { recursive: true }));
fs.writeFileSync(dir + '/a/hello.txt', 'hello');
fs.appendFileSync(dir + '/a/hello.txt', ' world');
log('read: ' + fs.readFileSync(dir + '/a/hello.txt'));
log('read utf8: ' + fs.readFileSync(dir + '/a/hello.txt', { encoding: 'utf8' }));
fs.writeFileSync(dir + '/a/hello.txt', 'hello world', { flag: 'w' });
var st = fs.statSync(dir + '/a/hello.txt');
log(
  'stat: size=' +
    st.size +
    ' file=' +
    st.isFile() +
    ' dir=' +
    st.isDirectory() +
    ' link=' +
    st.isSymbolicLink() +
    ' mode=' +
    (st.mode & 0o777).toString(8) +
    ' mtime=' +
    (st.mtime instanceof Date && st.mtime.getTime() === Math.floor(st.mtimeMs)) +
    ' Stats=' +
    (st instanceof fs.Stats)
);
log('stat dir: ' + fs.statSync(dir + '/a').isDirectory());
log('readdir: ' + JSON.stringify(fs.readdirSync(dir + '/a')));
log(
  'readdir types: ' +
    fs
      .readdirSync(dir + '/a', { withFileTypes: true })
      .map(function (d) {
        return d.name + ':' + (d.isDirectory() ? 'd' : d.isFile() ? 'f' : '?') + ':' + rel(d.parentPath);
      })
      .join(',')
);
log('readdir recursive: ' + JSON.stringify(fs.readdirSync(dir, { recursive: true })));
log('exists: ' + fs.existsSync(dir + '/a/hello.txt') + ' ' + fs.existsSync(dir + '/nope') + ' ' + fs.existsSync(42));
fs.copyFileSync(dir + '/a/hello.txt', dir + '/a/copy.txt');
fs.renameSync(dir + '/a/copy.txt', dir + '/a/moved.txt');
log('moved: ' + fs.readFileSync(dir + '/a/moved.txt'));
fs.symlinkSync(dir + '/a/moved.txt', dir + '/a/link');
log('readlink: ' + (fs.readlinkSync(dir + '/a/link') === dir + '/a/moved.txt'));
log('lstat link: ' + fs.lstatSync(dir + '/a/link').isSymbolicLink() + ' stat link: ' + fs.statSync(dir + '/a/link').isFile());
log('realpath: ' + (fs.realpathSync(dir + '/a/link') === fs.realpathSync(dir + '/a/moved.txt')));
fs.chmodSync(dir + '/a/moved.txt', 0o600);
log('chmod: ' + (fs.statSync(dir + '/a/moved.txt').mode & 0o777).toString(8));
fs.chmodSync(dir + '/a/moved.txt', '644');
log('chmod octal string: ' + (fs.statSync(dir + '/a/moved.txt').mode & 0o777).toString(8));
fs.accessSync(dir + '/a/moved.txt', fs.constants.R_OK | fs.constants.W_OK);
log('access ok');
fs.truncateSync(dir + '/a/moved.txt', 5);
log('truncated: ' + fs.readFileSync(dir + '/a/moved.txt'));
var tmp = fs.mkdtempSync(dir + '/tmp-');
log('mkdtemp: ' + (tmp.indexOf(dir + '/tmp-') === 0 && tmp.length === dir.length + 11) + ' ' + fs.statSync(tmp).isDirectory());
fs.utimesSync(dir + '/a/moved.txt', 1000, 2000);
log('utimes: ' + fs.statSync(dir + '/a/moved.txt').mtimeMs);
log('stat missing: ' + fs.statSync(dir + '/nope', { throwIfNoEntry: false }));
fs.unlinkSync(dir + '/a/link');
fs.rmdirSync(dir + '/a/b');
log('rmdir: ' + fs.existsSync(dir + '/a/b'));
log('constants: ' + fs.constants.F_OK + fs.constants.R_OK + fs.constants.W_OK + fs.constants.X_OK + ' ' + fs.constants.COPYFILE_EXCL);

function errInfo(fn) {
  try {
    fn();
    return 'no error';
  } catch (e) {
    return (
      e.code +
      ' ' +
      (e instanceof Error) +
      ' ' +
      (e instanceof TypeError) +
      (e.syscall ? ' ' + e.syscall : '') +
      (e.errno !== undefined ? ' errno=' + e.errno : '') +
      (e.path !== undefined ? ' path=' + rel(e.path) : '') +
      (e.dest !== undefined ? ' dest=' + rel(e.dest) : '')
    );
  }
}
log('err readFile: ' + errInfo(function () { fs.readFileSync(dir + '/nope'); }));
try {
  fs.readFileSync(dir + '/nope');
} catch (e) {
  log('err message: ' + e.message.replace(dir, '<dir>'));
}
log('err rmdir: ' + errInfo(function () { fs.rmdirSync(dir + '/a'); }));
log('err rmdir file: ' + errInfo(function () { fs.rmdirSync(dir + '/a/hello.txt'); }));
log('err rm dir: ' + errInfo(function () { fs.rmSync(dir + '/a'); }));
log('err rm missing: ' + errInfo(function () { fs.rmSync(dir + '/nope'); }));
log('rm force missing: ' + errInfo(function () { fs.rmSync(dir + '/nope', { force: true }); }));
log('err mkdir: ' + errInfo(function () { fs.mkdirSync(dir + '/a'); }));
log('err mkdir over file: ' + errInfo(function () { fs.mkdirSync(dir + '/a/hello.txt', { recursive: true }); }));
log('err readFile dir: ' + errInfo(function () { fs.readFileSync(dir + '/a'); }));
log('err unlink dir: ' + errInfo(function () { fs.unlinkSync(dir + '/a'); }));
log('err rename: ' + errInfo(function () { fs.renameSync(dir + '/nope', dir + '/nope2'); }));
log('err copy excl: ' + errInfo(function () { fs.copyFileSync(dir + '/a/hello.txt', dir + '/a/moved.txt', fs.constants.COPYFILE_EXCL); }));
log('err access: ' + errInfo(function () { fs.accessSync(dir + '/nope'); }));
log('err readlink: ' + errInfo(function () { fs.readlinkSync(dir + '/a/hello.txt'); }));
log('err wx: ' + errInfo(function () { fs.writeFileSync(dir + '/a/hello.txt', 'x', { flag: 'wx' }); }));
log('err path type: ' + errInfo(function () { fs.readFileSync(123); }));
log('err encoding: ' + errInfo(function () { fs.readFileSync(dir + '/a/hello.txt', 'latin1'); }));
log('err data type: ' + errInfo(function () { fs.writeFileSync(dir + '/x', 42); }));
log('err mode: ' + errInfo(function () { fs.chmodSync(dir + '/a/hello.txt', '9'); }));
log('err flag: ' + errInfo(function () { fs.writeFileSync(dir + '/x', 'x', { flag: 'q' }); }));
log('err callback: ' + errInfo(function () { fs.readFileSync(dir + '/a/hello.txt', function () {}); }));
log('err recursive watch: ' + errInfo(function () { fs.watch(dir, { recursive: true }, function () {}); }));
log('err watch missing: ' + errInfo(function () { fs.watch(dir + '/nope', function () {}); }));
// review-round cases
fs.copyFileSync(dir + '/a/hello.txt', dir + '/a/hello.txt');
fs.symlinkSync(dir + '/a/hello.txt', dir + '/a/hello-link');
fs.copyFileSync(dir + '/a/hello.txt', dir + '/a/hello-link');
log('copy onto itself: ' + fs.readFileSync(dir + '/a/hello.txt') + ' | ' + fs.readFileSync(dir + '/a/hello-link'));
fs.unlinkSync(dir + '/a/hello-link');
log('err rmdir recursive file: ' + errInfo(function () { fs.rmdirSync(dir + '/a/hello.txt', { recursive: true }); }));
log('readFile a+: [' + fs.readFileSync(dir + '/a/created-by-read.txt', { flag: 'a+' }) + '] ' + fs.existsSync(dir + '/a/created-by-read.txt'));
fs.unlinkSync(dir + '/a/created-by-read.txt');
log('err readFile flag: ' + errInfo(function () { fs.readFileSync(dir + '/a/hello.txt', { flag: 'q' }); }));
log('stat through file: ' + fs.statSync(dir + '/a/hello.txt/x', { throwIfNoEntry: false }));
log('err utimes NaN date: ' + errInfo(function () { fs.utimesSync(dir + '/a/hello.txt', new Date(NaN), 1); }));
log('err symlink type: ' + errInfo(function () { fs.symlinkSync(dir + '/a/hello.txt', dir + '/a/l2', 'bogus'); }));
log('err access range: ' + errInfo(function () { fs.accessSync(dir + '/a/hello.txt', 8); }) + ' ' + (function () { try { fs.accessSync(dir + '/a/hello.txt', 8); } catch (e) { return e instanceof RangeError; } })());
log('err copy ficlone force: ' + errInfo(function () { fs.copyFileSync(dir + '/a/hello.txt', dir + '/a/c2', fs.constants.COPYFILE_FICLONE_FORCE); }));
fs.symlinkSync(dir + '/a/loop-b', dir + '/a/loop-a');
fs.symlinkSync(dir + '/a/loop-a', dir + '/a/loop-b');
log('err realpath loop: ' + errInfo(function () { fs.realpathSync(dir + '/a/loop-a'); }));
fs.unlinkSync(dir + '/a/loop-a');
fs.unlinkSync(dir + '/a/loop-b');
fs.symlinkSync(dir + '/nowhere', dir + '/dangling');
log('err mkdir over dangling link: ' + errInfo(function () { fs.mkdirSync(dir + '/dangling', { recursive: true }); }));
fs.unlinkSync(dir + '/dangling');
fs.writeFileSync(dir + '/setgid.txt', 'x', { mode: 0o2640 });
log('setgid mode: ' + (fs.statSync(dir + '/setgid.txt').mode & 0o7777).toString(8));
fs.writeFileSync(dir + '/big.txt', '');
fs.truncateSync(dir + '/big.txt', 11 * 1024 * 1024);
log('err too large: ' + errInfo(function () { fs.readFileSync(dir + '/big.txt'); }) + ' ' + (function () { try { fs.readFileSync(dir + '/big.txt'); } catch (e) { return e instanceof RangeError; } })());
log('sync done');

// ---- the promise API (after this await the file is a top-level-await file)
log('async read: ' + (await fs.readFile(dir + '/a/hello.txt')));
await fs.writeFile(dir + '/a/async.txt', 'async');
await fs.appendFile(dir + '/a/async.txt', '!');
log('async append: ' + (await fs.readFile(dir + '/a/async.txt')));
log('async stat: ' + (await fs.stat(dir + '/a/async.txt')).size + ' ' + ((await fs.lstat(dir + '/a')).isDirectory()));
log('async readdir: ' + JSON.stringify(await fs.readdir(dir + '/a')));
log('async readdir types: ' + (await fs.readdir(dir + '/a', { withFileTypes: true })).map(function (d) { return d.name; }).join(','));
log('async exists: ' + (await fs.exists(dir + '/a/async.txt')) + ' ' + (await fs.exists(dir + '/nope')));
log('async mkdir: ' + rel(await fs.mkdir(dir + '/c/d', { recursive: true })) + ' ' + (await fs.mkdir(dir + '/c/e')));
await fs.copyFile(dir + '/a/async.txt', dir + '/c/copy.txt');
await fs.rename(dir + '/c/copy.txt', dir + '/c/moved.txt');
await fs.symlink(dir + '/c/moved.txt', dir + '/c/link');
log('async link: ' + (await fs.readlink(dir + '/c/link') === dir + '/c/moved.txt') + ' ' + (await fs.realpath(dir + '/c/link') === await fs.realpath(dir + '/c/moved.txt')));
await fs.chmod(dir + '/c/moved.txt', 0o640);
await fs.access(dir + '/c/moved.txt', fs.constants.R_OK);
await fs.truncate(dir + '/c/moved.txt', 2);
await fs.utimes(dir + '/c/moved.txt', 3000, 4000);
var mst = await fs.stat(dir + '/c/moved.txt');
log('async chmod/truncate/utimes: ' + (mst.mode & 0o777).toString(8) + ' ' + mst.size + ' ' + mst.mtimeMs);
log('async mkdtemp: ' + ((await fs.mkdtemp(dir + '/c/t-')).indexOf(dir + '/c/t-') === 0));
await fs.unlink(dir + '/c/link');
await fs.rmdir(dir + '/c/e');
log('async stat missing: ' + (await fs.stat(dir + '/nope', { throwIfNoEntry: false })));
log('promises alias: ' + (require('fs/promises').readFile === fs.promises.readFile) + ' ' + (fs.readFile === fs.promises.readFile) + ' ' + (require('node:fs') === fs) + ' ' + (require('node:fs/promises') === fs.promises));
try {
  await fs.readFile(dir + '/nope');
} catch (e) {
  log('async err: ' + e.code + ' ' + (e instanceof Error) + ' ' + e.syscall + ' errno=' + e.errno + ' path=' + rel(e.path) + ' | ' + e.message.replace(dir, '<dir>'));
}
try {
  await fs.rename(dir + '/nope', dir + '/nope2');
} catch (e) {
  log('async err rename: ' + e.code + ' dest=' + rel(e.dest));
}
try {
  await fs.readFile(dir + '/a/hello.txt', function () {});
} catch (e) {
  log('async err callback: ' + e.code + ' ' + (e instanceof TypeError));
}
try {
  await fs.readFile(123);
} catch (e) {
  log('async err path type: ' + e.code);
}
try {
  await fs.readFile(dir + '/big.txt');
} catch (e) {
  log('async err too large: ' + e.code + ' ' + (e instanceof RangeError));
}
await fs.rm(dir + '/c', { recursive: true });
log('async rm: ' + fs.existsSync(dir + '/c'));
await Promise.all([fs.writeFile(dir + '/p1', '1'), fs.writeFile(dir + '/p2', '2'), fs.writeFile(dir + '/p3', '3')]);
var parallel = await Promise.all([fs.readFile(dir + '/p1'), fs.readFile(dir + '/p2'), fs.readFile(dir + '/p3')]);
log('parallel: ' + parallel.join(','));
fs.rmSync(dir, { recursive: true });
log('done: ' + fs.existsSync(dir));
