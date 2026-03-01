/* global defineRule, fs, log */
/* eslint-disable no-restricted-syntax, no-unused-vars, security/detect-object-injection */
defineRule('fileCmd', {
  whenChanged: 'somedev/fileCmd',
  then: function (cmd) {
    var parts = cmd.split('|');
    var op = parts[0];

    try {
      if (op === 'readFile') {
        var content = fs.readFileSync(parts[1]);
        log('readFile: [{}]', content);
      } else if (op === 'writeFile') {
        fs.writeFileSync(parts[1], parts.slice(2).join('|'));
        log('writeFile: ok');
      } else if (op === 'appendFile') {
        fs.appendFileSync(parts[1], parts.slice(2).join('|'));
        log('appendFile: ok');
      } else if (op === 'stat') {
        var st = fs.statSync(parts[1]);
        log('stat: size={} isFile={} isDirectory={} mode={} mtime={}', st.size, st.isFile, st.isDirectory, st.mode, st.mtime);
      } else if (op === 'readDir') {
        var entries = fs.readdirSync(parts[1]);
        var names = [];
        for (var i = 0; i < entries.length; i++) {
          names.push(entries[i].name + '(file=' + entries[i].isFile + ',dir=' + entries[i].isDirectory + ')');
        }
        names.sort();
        log('readDir: {}', names.join(','));
      } else if (op === 'exists') {
        var result = fs.existsSync(parts[1]);
        log('exists: {}', result);
      } else if (op === 'mkdir') {
        if (parts[2] === 'recursive') {
          fs.mkdirSync(parts[1], {recursive: true});
        } else {
          fs.mkdirSync(parts[1]);
        }
        log('mkdir: ok');
      } else if (op === 'unlink') {
        fs.unlinkSync(parts[1]);
        log('unlink: ok');
      } else if (op === 'rename') {
        fs.renameSync(parts[1], parts[2]);
        log('rename: ok');
      } else if (op === 'readFileNoArgs') {
        fs.readFileSync();
        log('readFile: should not reach');
      } else if (op === 'writeFileOneArg') {
        fs.writeFileSync(parts[1]);
        log('writeFile: should not reach');
      } else if (op === 'statNoArgs') {
        fs.statSync();
        log('stat: should not reach');
      } else if (op === 'readFileInt') {
        fs.readFileSync(123);
        log('readFile: should not reach');
      } else if (op === 'unlinkDir') {
        fs.unlinkSync(parts[1]);
        log('unlink: should not reach');
      } else if (op === 'rmdir') {
        if (parts[2] === 'recursive') {
          fs.rmdirSync(parts[1], {recursive: true});
        } else {
          fs.rmdirSync(parts[1]);
        }
        log('rmdir: ok');
      } else if (op === 'copyFile') {
        fs.copyFileSync(parts[1], parts[2]);
        log('copyFile: ok');
      } else if (op === 'access') {
        if (parts[2]) {
          fs.accessSync(parts[1], parseInt(parts[2]));
        } else {
          fs.accessSync(parts[1]);
        }
        log('access: ok');
      } else if (op === 'realpath') {
        var resolved = fs.realpathSync(parts[1]);
        log('realpath: {}', resolved);
      } else if (op === 'readlink') {
        var target = fs.readlinkSync(parts[1]);
        log('readlink: {}', target);
      } else if (op === 'accessConstants') {
        log('F_OK={} R_OK={} W_OK={} X_OK={}', fs.constants.F_OK, fs.constants.R_OK, fs.constants.W_OK, fs.constants.X_OK);

      // Async operations
      } else if (op === 'asyncReadFile') {
        fs.readFile(parts[1], function (err, data) {
          if (err) {
            log('asyncReadFile error: {}', err.message);
          } else {
            log('asyncReadFile: [{}]', data);
          }
        });
      } else if (op === 'asyncWriteFile') {
        fs.writeFile(parts[1], parts.slice(2).join('|'), function (err) {
          if (err) {
            log('asyncWriteFile error: {}', err.message);
          } else {
            log('asyncWriteFile: ok');
          }
        });
      } else if (op === 'asyncAppendFile') {
        fs.appendFile(parts[1], parts.slice(2).join('|'), function (err) {
          if (err) {
            log('asyncAppendFile error: {}', err.message);
          } else {
            log('asyncAppendFile: ok');
          }
        });
      } else if (op === 'asyncStat') {
        fs.stat(parts[1], function (err, st) {
          if (err) {
            log('asyncStat error: {}', err.message);
          } else {
            log('asyncStat: size={} isFile={} isDirectory={}', st.size, st.isFile, st.isDirectory);
          }
        });
      } else if (op === 'asyncReaddir') {
        fs.readdir(parts[1], function (err, entries) {
          if (err) {
            log('asyncReaddir error: {}', err.message);
          } else {
            var names = [];
            for (var i = 0; i < entries.length; i++) {
              names.push(entries[i].name);
            }
            names.sort();
            log('asyncReaddir: {}', names.join(','));
          }
        });
      } else if (op === 'asyncExists') {
        fs.exists(parts[1], function (exists) {
          log('asyncExists: {}', exists);
        });
      } else if (op === 'asyncMkdir') {
        if (parts[2] === 'recursive') {
          fs.mkdir(parts[1], {recursive: true}, function (err) {
            if (err) {
              log('asyncMkdir error: {}', err.message);
            } else {
              log('asyncMkdir: ok');
            }
          });
        } else {
          fs.mkdir(parts[1], function (err) {
            if (err) {
              log('asyncMkdir error: {}', err.message);
            } else {
              log('asyncMkdir: ok');
            }
          });
        }
      } else if (op === 'asyncUnlink') {
        fs.unlink(parts[1], function (err) {
          if (err) {
            log('asyncUnlink error: {}', err.message);
          } else {
            log('asyncUnlink: ok');
          }
        });
      } else if (op === 'asyncRename') {
        fs.rename(parts[1], parts[2], function (err) {
          if (err) {
            log('asyncRename error: {}', err.message);
          } else {
            log('asyncRename: ok');
          }
        });
      } else if (op === 'asyncRmdir') {
        if (parts[2] === 'recursive') {
          fs.rmdir(parts[1], {recursive: true}, function (err) {
            if (err) {
              log('asyncRmdir error: {}', err.message);
            } else {
              log('asyncRmdir: ok');
            }
          });
        } else {
          fs.rmdir(parts[1], function (err) {
            if (err) {
              log('asyncRmdir error: {}', err.message);
            } else {
              log('asyncRmdir: ok');
            }
          });
        }
      } else if (op === 'asyncCopyFile') {
        fs.copyFile(parts[1], parts[2], function (err) {
          if (err) {
            log('asyncCopyFile error: {}', err.message);
          } else {
            log('asyncCopyFile: ok');
          }
        });
      } else if (op === 'asyncAccess') {
        if (parts[2]) {
          fs.access(parts[1], parseInt(parts[2]), function (err) {
            if (err) {
              log('asyncAccess error: {}', err.message);
            } else {
              log('asyncAccess: ok');
            }
          });
        } else {
          fs.access(parts[1], function (err) {
            if (err) {
              log('asyncAccess error: {}', err.message);
            } else {
              log('asyncAccess: ok');
            }
          });
        }
      } else if (op === 'asyncRealpath') {
        fs.realpath(parts[1], function (err, resolved) {
          if (err) {
            log('asyncRealpath error: {}', err.message);
          } else {
            log('asyncRealpath: {}', resolved);
          }
        });
      } else if (op === 'asyncReadlink') {
        fs.readlink(parts[1], function (err, target) {
          if (err) {
            log('asyncReadlink error: {}', err.message);
          } else {
            log('asyncReadlink: {}', target);
          }
        });
      } else if (op === 'watch') {
        var watcher = fs.watch(parts[1], function (eventType, filename) {
          log('watch: {} {}', eventType, filename);
        });
        // Store watcher globally so we can close it later
        global._testWatcher = watcher;
        log('watch: started');
      } else if (op === 'watchClose') {
        if (global._testWatcher) {
          global._testWatcher.close();
          log('watch: closed');
        }
      // Async wrong args
      } else if (op === 'asyncReadFileNoCallback') {
        fs.readFile(parts[1]);
        log('asyncReadFile: should not reach');
      } else if (op === 'watchNonExistent') {
        var w = fs.watch(parts[1], function (eventType, filename) {
          log('watch: {} {}', eventType, filename);
        });
        log('watch: should not reach');
      } else if (op === 'asyncWriteFileNoCallback') {
        fs.writeFile(parts[1], parts.slice(2).join('|'));
        log('asyncWriteFile: should not reach');
      } else if (op === 'asyncStatNoCallback') {
        fs.stat(parts[1]);
        log('asyncStat: should not reach');
      }
    } catch (e) {
      log.error('caught error');
    }
  },
});
