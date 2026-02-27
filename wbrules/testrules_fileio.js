defineRule('fileCmd', {
  whenChanged: 'somedev/fileCmd',
  then: function (cmd) {
    var parts = cmd.split('|');
    var op = parts[0];

    try {
      if (op === 'readFile') {
        var content = fs.readFile(parts[1]);
        log('readFile: {}', content);
      } else if (op === 'writeFile') {
        fs.writeFile(parts[1], parts[2]);
        log('writeFile: ok');
      } else if (op === 'appendFile') {
        fs.appendFile(parts[1], parts[2]);
        log('appendFile: ok');
      } else if (op === 'stat') {
        var st = fs.stat(parts[1]);
        log('stat: size={} isFile={} isDirectory={}', st.size, st.isFile, st.isDirectory);
      } else if (op === 'readDir') {
        var entries = fs.readDir(parts[1]);
        var names = [];
        for (var i = 0; i < entries.length; i++) {
          names.push(entries[i].name + '(file=' + entries[i].isFile + ',dir=' + entries[i].isDirectory + ')');
        }
        names.sort();
        log('readDir: {}', names.join(','));
      } else if (op === 'exists') {
        var result = fs.exists(parts[1]);
        log('exists: {}', result);
      } else if (op === 'mkdir') {
        if (parts[2] === 'recursive') {
          fs.mkdir(parts[1], {recursive: true});
        } else {
          fs.mkdir(parts[1]);
        }
        log('mkdir: ok');
      } else if (op === 'unlink') {
        fs.unlink(parts[1]);
        log('unlink: ok');
      } else if (op === 'rename') {
        fs.rename(parts[1], parts[2]);
        log('rename: ok');
      }
    } catch (e) {
      log.error('caught error');
    }
  },
});
