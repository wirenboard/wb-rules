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
		fmt.Sprintf("[error] fs.readFile() failed: stat %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestReadFileEmptyString() {
	p := filepath.Join(s.tmpDir, "empty.txt")
	os.WriteFile(p, []byte(""), 0644)

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
	s.Equal("", string(data))
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
	os.WriteFile(p, []byte("12345"), 0644)

	info, err := os.Stat(p)
	s.Require().NoError(err)

	s.callCmd(
		fmt.Sprintf("stat|%s", p),
		fmt.Sprintf("[info] stat: size=5 isFile=true isDirectory=false mode=644 mtime=%d", info.ModTime().Unix()),
	)
}

func (s *RuleFileIOSuite) TestStatDirectory() {
	d := filepath.Join(s.tmpDir, "subdir")
	os.Mkdir(d, 0755)

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
		fmt.Sprintf("[error] fs.stat() failed: stat %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestReadDir() {
	os.WriteFile(filepath.Join(s.tmpDir, "aaa.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(s.tmpDir, "bbb.txt"), []byte("b"), 0644)
	os.Mkdir(filepath.Join(s.tmpDir, "ccc"), 0755)

	s.callCmd(
		fmt.Sprintf("readDir|%s", s.tmpDir),
		"[info] readDir: aaa.txt(file=true,dir=false),bbb.txt(file=true,dir=false),ccc(file=false,dir=true)",
	)
}

func (s *RuleFileIOSuite) TestReadDirNonExistent() {
	p := filepath.Join(s.tmpDir, "no_such_dir")
	s.callCmd(
		fmt.Sprintf("readDir|%s", p),
		fmt.Sprintf("[error] fs.readDir() failed: open %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestReadDirEmpty() {
	d := filepath.Join(s.tmpDir, "emptydir")
	os.Mkdir(d, 0755)

	s.callCmd(
		fmt.Sprintf("readDir|%s", d),
		"[info] readDir: ",
	)
}

func (s *RuleFileIOSuite) TestExists() {
	p := filepath.Join(s.tmpDir, "existing.txt")
	os.WriteFile(p, []byte("x"), 0644)

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
	os.Mkdir(d, 0755)

	s.callCmd(
		fmt.Sprintf("mkdir|%s", d),
		fmt.Sprintf("[error] fs.mkdir() failed: mkdir %s: file exists", d),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestUnlink() {
	p := filepath.Join(s.tmpDir, "todelete.txt")
	os.WriteFile(p, []byte("x"), 0644)

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
		fmt.Sprintf("[error] fs.unlink() failed: lstat %s: no such file or directory", p),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestUnlinkDirectory() {
	d := filepath.Join(s.tmpDir, "cantdelete")
	os.Mkdir(d, 0755)

	s.callCmd(
		fmt.Sprintf("unlinkDir|%s", d),
		fmt.Sprintf("[error] fs.unlink() failed: %s is a directory, use fs.rmdir() or remove manually", d),
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestRename() {
	oldPath := filepath.Join(s.tmpDir, "old.txt")
	newPath := filepath.Join(s.tmpDir, "new.txt")
	os.WriteFile(oldPath, []byte("content"), 0644)

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
		fmt.Sprintf("[error] fs.rename() failed: rename %s %s: no such file or directory", oldPath, newPath),
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
	// readFile with no args
	s.callCmd(
		"readFileNoArgs",
		"[error] fs.readFile(): expected (path)",
		"[error] caught error",
	)
	s.EnsureGotErrors()

	// writeFile with one arg
	p := filepath.Join(s.tmpDir, "test.txt")
	s.sendCmd(fmt.Sprintf("writeFileOneArg|%s", p))
	s.Verify(
		fmt.Sprintf("tst -> /devices/somedev/controls/fileCmd: [writeFileOneArg|%s] (QoS 1, retained)", p),
		"[error] fs.writeFile(): expected (path, data)",
		"[error] caught error",
	)
	s.EnsureGotErrors()

	// stat with no args
	s.sendCmd("statNoArgs")
	s.Verify(
		"tst -> /devices/somedev/controls/fileCmd: [statNoArgs] (QoS 1, retained)",
		"[error] fs.stat(): expected (path)",
		"[error] caught error",
	)
	s.EnsureGotErrors()
}

func TestRuleFileIOSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleFileIOSuite),
	)
}
