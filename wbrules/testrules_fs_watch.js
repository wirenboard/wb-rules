// fs.watch: a directory watcher and a file watcher on a sandbox next to
// this file; the suite drives the filesystem and reads the events back.
/* global log, __filename, defineRule, dev */
var fs = require('fs');
var base = __filename.slice(0, __filename.lastIndexOf('/'));
var dir = base + '/fs-watch';

fs.rmSync(dir, { recursive: true, force: true });
fs.mkdirSync(dir);
fs.writeFileSync(dir + '/file.txt', 'initial');

var dirWatcher = fs.watch(dir, function (eventType, filename) {
  log('dir event: ' + eventType + ' ' + filename);
});
var fileWatcher = fs.watch(dir + '/file.txt', function (eventType, filename) {
  log('file event: ' + eventType + ' ' + filename);
});
log('watching: ' + (dirWatcher instanceof fs.FSWatcher) + ' ' + (typeof fileWatcher.close));

var dir2Watcher = null;
defineRule('secondWatcher', {
  whenChanged: 'somedev/temp',
  then: function (value) {
    if (value === 21) {
      dirWatcher.close();
      log('first watcher closed');
      return;
    }
    dir2Watcher = fs.watch(dir, function (eventType, filename) {
      log('dir2 event: ' + eventType + ' ' + filename);
    });
    log('second watcher added');
  },
});

defineRule('closeWatchers', {
  whenChanged: 'somedev/sw',
  then: function () {
    dirWatcher.close();
    dirWatcher.close(); // idempotent
    fileWatcher.close();
    if (dir2Watcher) dir2Watcher.close();
    log('watchers closed');
  },
});
