package wbrules

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirenboard/wbgong/testutils"
)

// The built-in fs module: the synchronous API runs before the fixture's
// first await (its log lines precede the load notification), the promise
// API after it.
type RuleFsSuite struct {
	RuleSuiteBase
}

func (s *RuleFsSuite) SetupTest() {
	s.SetupSkippingDefs()
}

func (s *RuleFsSuite) TestFsModule() {
	s.Ck("LiveLoadScript", s.LiveLoadScript("testrules_fs.js"))
	s.Verify(
		"[info] mkdir: /fs-sandbox",
		"[info] mkdir again: undefined",
		"[info] read: hello world",
		"[info] read utf8: hello world",
		"[info] stat: size=11 file=true dir=false link=false mode=644 mtime=true Stats=true",
		"[info] stat dir: true",
		`[info] readdir: ["b","hello.txt"]`,
		"[info] readdir types: b:d:/fs-sandbox/a,hello.txt:f:/fs-sandbox/a",
		`[info] readdir recursive: ["a","a/b","a/hello.txt"]`,
		"[info] exists: true false false",
		"[info] moved: hello world",
		"[info] readlink: true",
		"[info] lstat link: true stat link: true",
		"[info] realpath: true",
		"[info] chmod: 600",
		"[info] chmod octal string: 644",
		"[info] access ok",
		"[info] truncated: hello",
		"[info] mkdtemp: true true",
		"[info] utimes: 2000000",
		"[info] stat missing: undefined",
		"[info] rmdir: false",
		"[info] constants: 0421 1",
		"[info] err readFile: ENOENT true false open errno=-2 path=/fs-sandbox/nope",
		"[info] err message: ENOENT: no such file or directory, open '<dir>/nope'",
		"[info] err rmdir: ENOTEMPTY true false rmdir errno=-39 path=/fs-sandbox/a",
		"[info] err rmdir file: ENOTDIR true false rmdir errno=-20 path=/fs-sandbox/a/hello.txt",
		"[info] err rm dir: ERR_FS_EISDIR true false rm path=/fs-sandbox/a",
		"[info] err rm missing: ENOENT true false lstat errno=-2 path=/fs-sandbox/nope",
		"[info] rm force missing: no error",
		"[info] err mkdir: EEXIST true false mkdir errno=-17 path=/fs-sandbox/a",
		"[info] err mkdir over file: EEXIST true false mkdir errno=-17 path=/fs-sandbox/a/hello.txt",
		"[info] err readFile dir: EISDIR true false read errno=-21 path=/fs-sandbox/a",
		"[info] err unlink dir: EISDIR true false unlink errno=-21 path=/fs-sandbox/a",
		"[info] err rename: ENOENT true false rename errno=-2 path=/fs-sandbox/nope dest=/fs-sandbox/nope2",
		"[info] err copy excl: EEXIST true false copyfile errno=-17 path=/fs-sandbox/a/hello.txt dest=/fs-sandbox/a/moved.txt",
		"[info] err access: ENOENT true false access errno=-2 path=/fs-sandbox/nope",
		"[info] err readlink: EINVAL true false readlink errno=-22 path=/fs-sandbox/a/hello.txt",
		"[info] err wx: EEXIST true false open errno=-17 path=/fs-sandbox/a/hello.txt",
		"[info] err path type: ERR_INVALID_ARG_TYPE true true",
		"[info] err encoding: ERR_INVALID_ARG_VALUE true true",
		"[info] err data type: ERR_INVALID_ARG_TYPE true true",
		"[info] err mode: ERR_INVALID_ARG_VALUE true true",
		"[info] err flag: ERR_INVALID_ARG_VALUE true true",
		"[info] err callback: ERR_INVALID_ARG_TYPE true true",
		"[info] err recursive watch: ERR_FEATURE_UNAVAILABLE_ON_PLATFORM true true",
		"[info] err watch missing: ENOENT true false watch errno=-2 path=/fs-sandbox/nope",
		"[info] sync done",
	)
	s.SkipTill("[changed] testrules_fs.js")
	s.Verify(
		"[info] async read: hello world",
		"[info] async append: async!",
		"[info] async stat: 6 true",
		`[info] async readdir: ["async.txt","hello.txt","moved.txt"]`,
		"[info] async readdir types: async.txt,hello.txt,moved.txt",
		"[info] async exists: true false",
		"[info] async mkdir: /fs-sandbox/c undefined",
		"[info] async link: true true",
		"[info] async chmod/truncate/utimes: 640 2 4000000",
		"[info] async mkdtemp: true",
		"[info] async stat missing: undefined",
		"[info] promises alias: true true true true",
		"[info] async err: ENOENT true open errno=-2 path=/fs-sandbox/nope | ENOENT: no such file or directory, open '<dir>/nope'",
		"[info] async err rename: ENOENT dest=/fs-sandbox/nope2",
		"[info] async err callback: ERR_INVALID_ARG_TYPE true",
		"[info] async err path type: ERR_INVALID_ARG_TYPE",
		"[info] async rm: false",
		"[info] parallel: 1,2,3",
		"[info] done: false",
	)
	s.VerifyEmpty()
}

// The module is per file: a second file gets its own instance and its own
// promises; both spellings resolve within one file.
func (s *RuleFsSuite) TestFsModuleShadowsFiles() {
	// a user module named fs.js in the module path must not replace the
	// built-in one
	s.ModulesPath = filepath.Join(s.DataFileTempDir(), "modules")
	s.Ck("mkdir", os.MkdirAll(s.ModulesPath, 0o755))
	s.Ck("write", os.WriteFile(filepath.Join(s.ModulesPath, "fs.js"), []byte("exports.shadow = true;\n"), 0o644))
	script := filepath.Join(s.DataFileTempDir(), "testrules_fs_shadow.js")
	s.Ck("write", os.WriteFile(script, []byte(
		"var fs = require('fs');\nlog('shadow: ' + (fs.shadow === undefined) + ' ' + (typeof fs.readFileSync));\n"), 0o644))
	s.Ck("LiveLoadScript", s.engine.LiveLoadFile(script))
	s.Verify("[info] shadow: true function")
	s.SkipTill("[changed] testrules_fs_shadow.js")
}

func TestRuleFsSuite(t *testing.T) {
	testutils.RunSuites(t, new(RuleFsSuite))
}

// fs.watch over inotify: events reach the listener, close() stops them,
// the file's reload/removal and the engine's Stop close what is left.
type RuleFsWatchSuite struct {
	RuleSuiteBase
}

func (s *RuleFsWatchSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_fs_watch.js")
}

func (s *RuleFsWatchSuite) sandbox() string {
	return filepath.Join(s.DataFileTempDir(), "fs-watch")
}

func (s *RuleFsWatchSuite) TestWatchEvents() {
	s.Equal(2, s.engine.FsWatcherCount())

	// a new entry in the watched directory: IN_CREATE -> rename, then the
	// write -> change (both for the directory watcher only)
	s.Ck("write", os.WriteFile(filepath.Join(s.sandbox(), "new.txt"), []byte("x"), 0o644))
	s.Verify(
		"[info] dir event: rename new.txt",
		"[info] dir event: change new.txt",
	)

	// writing the watched file: the directory watcher reports the entry,
	// the file watcher reports its own basename (the truncate and the write
	// may arrive as one or two modify events)
	s.Ck("write", os.WriteFile(filepath.Join(s.sandbox(), "file.txt"), []byte("changed"), 0o644))
	s.SkipTill("[info] file event: change file.txt")
	// drain whatever the second modify produced before the next step
	s.WaitFor(func() bool { return true })
	s.Ck("remove", os.Remove(filepath.Join(s.sandbox(), "new.txt")))
	s.SkipTill("[info] dir event: rename new.txt")

	// deleting the watched file: rename, then the kernel drops the watch
	// and the watcher frees itself
	s.Ck("remove", os.Remove(filepath.Join(s.sandbox(), "file.txt")))
	s.SkipTill("[info] file event: rename file.txt")
	s.WaitFor(func() bool { return s.engine.FsWatcherCount() == 1 })

	// close() from the script: no more events afterwards
	s.publish("/devices/somedev/controls/sw", "1", "somedev/sw")
	s.SkipTill("[info] watchers closed")
	s.Equal(0, s.engine.FsWatcherCount())
	s.Ck("write", os.WriteFile(filepath.Join(s.sandbox(), "after-close.txt"), []byte("x"), 0o644))
	time.Sleep(200 * time.Millisecond)
	s.VerifyEmpty()
}

func (s *RuleFsWatchSuite) TestWatchClosedOnReload() {
	s.Equal(2, s.engine.FsWatcherCount())
	s.RemoveScript("testrules_fs_watch.js")
	s.SkipTill("[removed] testrules_fs_watch.js")
	s.Equal(0, s.engine.FsWatcherCount(), "file removal must close its watchers")
	// events for the sandbox are no longer delivered
	s.Ck("write", os.WriteFile(filepath.Join(s.sandbox(), "orphan.txt"), []byte("x"), 0o644))
	time.Sleep(200 * time.Millisecond)
	s.VerifyEmpty()
}

func (s *RuleFsWatchSuite) TestWatchClosedOnStop() {
	s.Equal(2, s.engine.FsWatcherCount())
	s.engine.Stop()
	s.Equal(0, s.engine.FsWatcherCount(), "Stop must close every watcher")
	s.engine.Start()
	// a restart does not re-run loaded files (their timers are gone the
	// same way); reloading the file recreates its watchers
	s.Equal(0, s.engine.FsWatcherCount())
	s.RemoveScript("testrules_fs_watch.js")
	s.SkipTill("[removed] testrules_fs_watch.js")
	s.Ck("LiveLoadScript", s.LiveLoadScript("testrules_fs_watch.js"))
	s.SkipTill("[changed] testrules_fs_watch.js")
	s.Equal(2, s.engine.FsWatcherCount())
}

func TestRuleFsWatchSuite(t *testing.T) {
	testutils.RunSuites(t, new(RuleFsWatchSuite))
}
