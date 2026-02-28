package wbrules

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleFileIOSuite struct {
	RuleSuiteBase
	tmpDir  string
	cleanup func()
}

func (s *RuleFileIOSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_fileio.js")
	s.tmpDir, s.cleanup = testutils.SetupTempDir(s.T())
}

func (s *RuleFileIOSuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
	s.RuleSuiteBase.TearDownTest()
}

func (s *RuleFileIOSuite) sendCmd(cmd string) {
	s.publish("/devices/somedev/controls/fileCmd", cmd, "somedev/fileCmd")
}

// callCmd publishes meta/type, initial value, and actual command, then verifies all messages + expected log output.
// This follows the pattern from rule_shell_command_test.go to handle "do not trigger whenChanged on first published value".
func (s *RuleFileIOSuite) callCmd(cmd string, expectedMsgs ...interface{}) {
	s.publish("/devices/somedev/controls/fileCmd/meta/type", "text", "somedev/fileCmd")
	s.publish("/devices/somedev/controls/fileCmd", "initial_text", "somedev/fileCmd")
	s.sendCmd(cmd)

	msgs := []interface{}{
		"tst -> /devices/somedev/controls/fileCmd/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/fileCmd: [initial_text] (QoS 1, retained)",
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [%s] (QoS 1, retained)", cmd),
	}
	msgs = append(msgs, expectedMsgs...)
	s.Verify(msgs...)
}

// ──────────────────────────────────────────────
// Sync tests
// ──────────────────────────────────────────────

func (s *RuleFileIOSuite) TestWriteAndReadFile() {
	p := filepath.Join(s.tmpDir, "test.txt")

	s.callCmd(
		fmt.Sprintf("writeFile|%s|hello world", p),
		"[info] writeFile: ok",
	)

	s.sendCmd(fmt.Sprintf("readFile|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [readFile|%s] (QoS 1, retained)", p),
		"[info] readFile: [hello world]",
	)
}

func (s *RuleFileIOSuite) TestReadFileNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	s.callCmd(
		fmt.Sprintf("readFile|%s", p),
		fmt.Sprintf("[error] fs.readFileSync() failed: stat %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestReadFileEmptyString() {
	p := filepath.Join(s.tmpDir, "empty.txt")
	os.WriteFile(p, []byte(""), 0o644)

	s.callCmd(
		fmt.Sprintf("readFile|%s", p),
		"[info] readFile: []",
	)
}

func (s *RuleFileIOSuite) TestWriteFileEmptyString() {
	p := filepath.Join(s.tmpDir, "empty_write.txt")
	s.callCmd(
		fmt.Sprintf("writeFile|%s|", p),
		"[info] writeFile: ok",
	)

	data, err := os.ReadFile(p)
	s.Require().NoError(err)
	s.Empty(string(data))
}

func (s *RuleFileIOSuite) TestAppendFile() {
	p := filepath.Join(s.tmpDir, "append.txt")

	s.callCmd(
		fmt.Sprintf("writeFile|%s|first", p),
		"[info] writeFile: ok",
	)

	s.sendCmd(fmt.Sprintf("appendFile|%s| second", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [appendFile|%s| second] (QoS 1, retained)", p),
		"[info] appendFile: ok",
	)

	s.sendCmd(fmt.Sprintf("readFile|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [readFile|%s] (QoS 1, retained)", p),
		"[info] readFile: [first second]",
	)
}

func (s *RuleFileIOSuite) TestAppendFileNonExistent() {
	p := filepath.Join(s.tmpDir, "append_new.txt")
	s.callCmd(
		fmt.Sprintf("appendFile|%s|created by append", p),
		"[info] appendFile: ok",
	)

	data, err := os.ReadFile(p)
	s.Require().NoError(err)
	s.Equal("created by append", string(data))
}

func (s *RuleFileIOSuite) TestStat() {
	p := filepath.Join(s.tmpDir, "statfile.txt")
	os.WriteFile(p, []byte("12345"), 0o644)

	info, err := os.Stat(p)
	s.Require().NoError(err)

	s.callCmd(
		fmt.Sprintf("stat|%s", p),
		fmt.Sprintf("[info] stat: size=5 isFile=true isDirectory=false mode=644 mtime=%d", info.ModTime().Unix()),
	)
}

func (s *RuleFileIOSuite) TestStatDirectory() {
	d := filepath.Join(s.tmpDir, "subdir")
	os.Mkdir(d, 0o755)

	info, err := os.Stat(d)
	s.Require().NoError(err)

	s.callCmd(
		fmt.Sprintf("stat|%s", d),
		fmt.Sprintf("[info] stat: size=%d isFile=false isDirectory=true mode=755 mtime=%d", info.Size(), info.ModTime().Unix()),
	)
}

func (s *RuleFileIOSuite) TestStatNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent")
	s.callCmd(
		fmt.Sprintf("stat|%s", p),
		fmt.Sprintf("[error] fs.statSync() failed: stat %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestReadDir() {
	os.WriteFile(filepath.Join(s.tmpDir, "aaa.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(s.tmpDir, "bbb.txt"), []byte("b"), 0o644)
	os.Mkdir(filepath.Join(s.tmpDir, "ccc"), 0o755)

	s.callCmd(
		fmt.Sprintf("readDir|%s", s.tmpDir),
		"[info] readDir: aaa.txt(file=true,dir=false),bbb.txt(file=true,dir=false),ccc(file=false,dir=true)",
	)
}

func (s *RuleFileIOSuite) TestReadDirNonExistent() {
	p := filepath.Join(s.tmpDir, "no_such_dir")
	s.callCmd(
		fmt.Sprintf("readDir|%s", p),
		fmt.Sprintf("[error] fs.readdirSync() failed: open %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestReadDirEmpty() {
	d := filepath.Join(s.tmpDir, "emptydir")
	os.Mkdir(d, 0o755)

	s.callCmd(
		fmt.Sprintf("readDir|%s", d),
		"[info] readDir: ",
	)
}

func (s *RuleFileIOSuite) TestExists() {
	p := filepath.Join(s.tmpDir, "existing.txt")
	os.WriteFile(p, []byte("x"), 0o644)

	s.callCmd(
		fmt.Sprintf("exists|%s", p),
		"[info] exists: true",
	)
}

func (s *RuleFileIOSuite) TestExistsNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	s.callCmd(
		fmt.Sprintf("exists|%s", p),
		"[info] exists: false",
	)
}

func (s *RuleFileIOSuite) TestMkdir() {
	d := filepath.Join(s.tmpDir, "newdir")
	s.callCmd(
		fmt.Sprintf("mkdir|%s", d),
		"[info] mkdir: ok",
	)

	info, err := os.Stat(d)
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *RuleFileIOSuite) TestMkdirRecursive() {
	d := filepath.Join(s.tmpDir, "a", "b", "c")
	s.callCmd(
		fmt.Sprintf("mkdir|%s|recursive", d),
		"[info] mkdir: ok",
	)

	info, err := os.Stat(d)
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *RuleFileIOSuite) TestMkdirAlreadyExists() {
	d := filepath.Join(s.tmpDir, "existingdir")
	os.Mkdir(d, 0o755)

	s.callCmd(
		fmt.Sprintf("mkdir|%s", d),
		fmt.Sprintf("[error] fs.mkdirSync() failed: mkdir %s: file exists", d),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestUnlink() {
	p := filepath.Join(s.tmpDir, "todelete.txt")
	os.WriteFile(p, []byte("x"), 0o644)

	s.callCmd(
		fmt.Sprintf("unlink|%s", p),
		"[info] unlink: ok",
	)

	_, err := os.Stat(p)
	s.True(os.IsNotExist(err))
}

func (s *RuleFileIOSuite) TestUnlinkNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	s.callCmd(
		fmt.Sprintf("unlink|%s", p),
		fmt.Sprintf("[error] fs.unlinkSync() failed: lstat %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestUnlinkDirectory() {
	d := filepath.Join(s.tmpDir, "cantdelete")
	os.Mkdir(d, 0o755)

	s.callCmd(
		fmt.Sprintf("unlinkDir|%s", d),
		fmt.Sprintf("[error] fs.unlinkSync() failed: %s is a directory, use fs.rmdir() or remove manually", d),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestRename() {
	oldPath := filepath.Join(s.tmpDir, "old.txt")
	newPath := filepath.Join(s.tmpDir, "new.txt")
	os.WriteFile(oldPath, []byte("content"), 0o644)

	s.callCmd(
		fmt.Sprintf("rename|%s|%s", oldPath, newPath),
		"[info] rename: ok",
	)

	_, err := os.Stat(oldPath)
	s.True(os.IsNotExist(err))

	data, err := os.ReadFile(newPath)
	s.Require().NoError(err)
	s.Equal("content", string(data))
}

func (s *RuleFileIOSuite) TestRenameNonExistent() {
	oldPath := filepath.Join(s.tmpDir, "no_such_file.txt")
	newPath := filepath.Join(s.tmpDir, "new.txt")
	s.callCmd(
		fmt.Sprintf("rename|%s|%s", oldPath, newPath),
		fmt.Sprintf("[error] fs.renameSync() failed: rename %s %s: no such file or directory", oldPath, newPath),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestWriteFileOverwrite() {
	p := filepath.Join(s.tmpDir, "overwrite.txt")

	s.callCmd(
		fmt.Sprintf("writeFile|%s|original", p),
		"[info] writeFile: ok",
	)

	s.sendCmd(fmt.Sprintf("writeFile|%s|updated", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [writeFile|%s|updated] (QoS 1, retained)", p),
		"[info] writeFile: ok",
	)

	s.sendCmd(fmt.Sprintf("readFile|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [readFile|%s] (QoS 1, retained)", p),
		"[info] readFile: [updated]",
	)
}

func (s *RuleFileIOSuite) TestWrongArgTypes() {
	// readFileSync with no args
	s.callCmd(
		"readFileNoArgs",
		"[error] fs.readFileSync(): expected (path)",
		"[error] caught error",
	)
	s.EnsureGotErrors()

	// writeFileSync with one arg
	p := filepath.Join(s.tmpDir, "test.txt")
	s.sendCmd(fmt.Sprintf("writeFileOneArg|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [writeFileOneArg|%s] (QoS 1, retained)", p),
		"[error] fs.writeFileSync(): expected (path, data)",
		"[error] caught error",
	)
	s.EnsureGotErrors()

	// statSync with no args
	s.sendCmd("statNoArgs")
	s.Verify(
		"tst -> /devices/somedev/controls/fileCmd: [statNoArgs] (QoS 1, retained)",
		"[error] fs.statSync(): expected (path)",
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

// ──────────────────────────────────────────────
// Async tests
// ──────────────────────────────────────────────

func (s *RuleFileIOSuite) TestAsyncWriteAndReadFile() {
	p := filepath.Join(s.tmpDir, "async_test.txt")

	s.callCmd(
		fmt.Sprintf("asyncWriteFile|%s|async hello", p),
		"[info] asyncWriteFile: ok",
	)

	s.sendCmd(fmt.Sprintf("asyncReadFile|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [asyncReadFile|%s] (QoS 1, retained)", p),
		"[info] asyncReadFile: [async hello]",
	)
}

func (s *RuleFileIOSuite) TestAsyncReadFileNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	s.callCmd(
		fmt.Sprintf("asyncReadFile|%s", p),
		fmt.Sprintf("[info] asyncReadFile error: stat %s: no such file or directory", p),
	)
}

func (s *RuleFileIOSuite) TestAsyncWriteFileError() {
	// Writing to a non-existent directory should produce an error
	p := filepath.Join(s.tmpDir, "no_such_dir", "file.txt")
	s.callCmd(
		fmt.Sprintf("asyncWriteFile|%s|data", p),
		fmt.Sprintf("[info] asyncWriteFile error: open %s: no such file or directory", p),
	)
}

func (s *RuleFileIOSuite) TestAsyncAppendFileError() {
	// Appending to a file in a non-existent directory should produce an error
	p := filepath.Join(s.tmpDir, "no_such_dir", "file.txt")
	s.callCmd(
		fmt.Sprintf("asyncAppendFile|%s|data", p),
		fmt.Sprintf("[info] asyncAppendFile error: open %s: no such file or directory", p),
	)
}

func (s *RuleFileIOSuite) TestAsyncAppendFile() {
	p := filepath.Join(s.tmpDir, "async_append.txt")

	s.callCmd(
		fmt.Sprintf("asyncWriteFile|%s|first", p),
		"[info] asyncWriteFile: ok",
	)

	s.sendCmd(fmt.Sprintf("asyncAppendFile|%s| second", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [asyncAppendFile|%s| second] (QoS 1, retained)", p),
		"[info] asyncAppendFile: ok",
	)

	s.sendCmd(fmt.Sprintf("asyncReadFile|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [asyncReadFile|%s] (QoS 1, retained)", p),
		"[info] asyncReadFile: [first second]",
	)
}

func (s *RuleFileIOSuite) TestAsyncStat() {
	p := filepath.Join(s.tmpDir, "async_stat.txt")
	os.WriteFile(p, []byte("12345"), 0o644)

	s.callCmd(
		fmt.Sprintf("asyncStat|%s", p),
		"[info] asyncStat: size=5 isFile=true isDirectory=false",
	)
}

func (s *RuleFileIOSuite) TestAsyncStatNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent")
	s.callCmd(
		fmt.Sprintf("asyncStat|%s", p),
		fmt.Sprintf("[info] asyncStat error: stat %s: no such file or directory", p),
	)
}

func (s *RuleFileIOSuite) TestAsyncReaddir() {
	os.WriteFile(filepath.Join(s.tmpDir, "aaa.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(s.tmpDir, "bbb.txt"), []byte("b"), 0o644)

	s.callCmd(
		fmt.Sprintf("asyncReaddir|%s", s.tmpDir),
		"[info] asyncReaddir: aaa.txt,bbb.txt",
	)
}

func (s *RuleFileIOSuite) TestAsyncReaddirNonExistent() {
	p := filepath.Join(s.tmpDir, "no_such_dir")
	s.callCmd(
		fmt.Sprintf("asyncReaddir|%s", p),
		fmt.Sprintf("[info] asyncReaddir error: open %s: no such file or directory", p),
	)
}

func (s *RuleFileIOSuite) TestAsyncExists() {
	p := filepath.Join(s.tmpDir, "async_exists.txt")
	os.WriteFile(p, []byte("x"), 0o644)

	s.callCmd(
		fmt.Sprintf("asyncExists|%s", p),
		"[info] asyncExists: true",
	)
}

func (s *RuleFileIOSuite) TestAsyncExistsNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	s.callCmd(
		fmt.Sprintf("asyncExists|%s", p),
		"[info] asyncExists: false",
	)
}

func (s *RuleFileIOSuite) TestAsyncMkdir() {
	d := filepath.Join(s.tmpDir, "async_newdir")
	s.callCmd(
		fmt.Sprintf("asyncMkdir|%s", d),
		"[info] asyncMkdir: ok",
	)

	info, err := os.Stat(d)
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *RuleFileIOSuite) TestAsyncMkdirRecursive() {
	d := filepath.Join(s.tmpDir, "a", "b", "c")
	s.callCmd(
		fmt.Sprintf("asyncMkdir|%s|recursive", d),
		"[info] asyncMkdir: ok",
	)

	info, err := os.Stat(d)
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *RuleFileIOSuite) TestAsyncMkdirAlreadyExists() {
	d := filepath.Join(s.tmpDir, "existingdir")
	os.Mkdir(d, 0o755)

	s.callCmd(
		fmt.Sprintf("asyncMkdir|%s", d),
		fmt.Sprintf("[info] asyncMkdir error: mkdir %s: file exists", d),
	)
}

func (s *RuleFileIOSuite) TestAsyncUnlink() {
	p := filepath.Join(s.tmpDir, "async_todelete.txt")
	os.WriteFile(p, []byte("x"), 0o644)

	s.callCmd(
		fmt.Sprintf("asyncUnlink|%s", p),
		"[info] asyncUnlink: ok",
	)

	_, err := os.Stat(p)
	s.True(os.IsNotExist(err))
}

func (s *RuleFileIOSuite) TestAsyncUnlinkNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	s.callCmd(
		fmt.Sprintf("asyncUnlink|%s", p),
		fmt.Sprintf("[info] asyncUnlink error: lstat %s: no such file or directory", p),
	)
}

func (s *RuleFileIOSuite) TestAsyncRename() {
	oldPath := filepath.Join(s.tmpDir, "async_old.txt")
	newPath := filepath.Join(s.tmpDir, "async_new.txt")
	os.WriteFile(oldPath, []byte("content"), 0o644)

	s.callCmd(
		fmt.Sprintf("asyncRename|%s|%s", oldPath, newPath),
		"[info] asyncRename: ok",
	)

	_, err := os.Stat(oldPath)
	s.True(os.IsNotExist(err))

	data, err := os.ReadFile(newPath)
	s.Require().NoError(err)
	s.Equal("content", string(data))
}

func (s *RuleFileIOSuite) TestAsyncRenameNonExistent() {
	oldPath := filepath.Join(s.tmpDir, "no_such_file.txt")
	newPath := filepath.Join(s.tmpDir, "new.txt")
	s.callCmd(
		fmt.Sprintf("asyncRename|%s|%s", oldPath, newPath),
		fmt.Sprintf("[info] asyncRename error: rename %s %s: no such file or directory", oldPath, newPath),
	)
}

func (s *RuleFileIOSuite) TestAsyncWrongArgTypes() {
	// readFile without callback
	p := filepath.Join(s.tmpDir, "test.txt")
	s.callCmd(
		fmt.Sprintf("asyncReadFileNoCallback|%s", p),
		"[error] fs.readFile(): expected (path, callback)",
		"[error] caught error",
	)
	s.EnsureGotErrors()

	// writeFile without callback
	s.sendCmd(fmt.Sprintf("asyncWriteFileNoCallback|%s|data", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [asyncWriteFileNoCallback|%s|data] (QoS 1, retained)", p),
		"[error] fs.writeFile(): expected (path, data, callback)",
		"[error] caught error",
	)
	s.EnsureGotErrors()

	// stat without callback
	s.sendCmd(fmt.Sprintf("asyncStatNoCallback|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [asyncStatNoCallback|%s] (QoS 1, retained)", p),
		"[error] fs.stat(): expected (path, callback)",
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func TestRuleFileIOSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleFileIOSuite),
	)
}
