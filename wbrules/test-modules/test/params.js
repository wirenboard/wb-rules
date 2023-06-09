exports.params = function params() {
  return '__filename: ' + __filename + ', module.filename: ' + module.filename;
};

log('Module params init');
