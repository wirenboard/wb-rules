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
	s.publish("/devices/somedev/controls/fileCmd/meta/type", "text", "somedev/fileCmd")
	s.Verify("tst -> /devices/somedev/controls/fileCmd/meta/type: [text] (QoS 1, retained)")
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

func (s *RuleFileIOSuite) verifyLog(cmd string, msgs ...interface{}) {
	msgs = append([]interface{}{
		fmt.Sprintf(
			"tst -> /devices/somedev/controls/fileCmd: [%s] (QoS 1, retained)",
			cmd),
	}, msgs...)
	s.Verify(msgs...)
}

func (s *RuleFileIOSuite) TestWriteAndReadFile() {
	p := filepath.Join(s.tmpDir, "test.txt")
	cmd := fmt.Sprintf("writeFile|%s|hello world", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] writeFile: ok")

	cmd = fmt.Sprintf("readFile|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] readFile: hello world")
}

func (s *RuleFileIOSuite) TestReadFileNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	cmd := fmt.Sprintf("readFile|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd,
		fmt.Sprintf("[error] fs.readFile() failed: open %s: no such file or directory", p),
		"[error] caught error")
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestAppendFile() {
	p := filepath.Join(s.tmpDir, "append.txt")
	cmd := fmt.Sprintf("writeFile|%s|first", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] writeFile: ok")

	cmd = fmt.Sprintf("appendFile|%s| second", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] appendFile: ok")

	cmd = fmt.Sprintf("readFile|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] readFile: first second")
}

func (s *RuleFileIOSuite) TestStat() {
	p := filepath.Join(s.tmpDir, "statfile.txt")
	os.WriteFile(p, []byte("12345"), 0644)

	cmd := fmt.Sprintf("stat|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] stat: size=5 isFile=true isDirectory=false")
}

func (s *RuleFileIOSuite) TestStatDirectory() {
	d := filepath.Join(s.tmpDir, "subdir")
	os.Mkdir(d, 0755)

	info, err := os.Stat(d)
	s.Require().NoError(err)

	cmd := fmt.Sprintf("stat|%s", d)
	s.sendCmd(cmd)
	s.verifyLog(cmd, fmt.Sprintf("[info] stat: size=%d isFile=false isDirectory=true", info.Size()))
}

func (s *RuleFileIOSuite) TestStatNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent")
	cmd := fmt.Sprintf("stat|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd,
		fmt.Sprintf("[error] fs.stat() failed: stat %s: no such file or directory", p),
		"[error] caught error")
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestReadDir() {
	os.WriteFile(filepath.Join(s.tmpDir, "aaa.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(s.tmpDir, "bbb.txt"), []byte("b"), 0644)
	os.Mkdir(filepath.Join(s.tmpDir, "ccc"), 0755)

	cmd := fmt.Sprintf("readDir|%s", s.tmpDir)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] readDir: aaa.txt(file=true,dir=false),bbb.txt(file=true,dir=false),ccc(file=false,dir=true)")
}

func (s *RuleFileIOSuite) TestExists() {
	p := filepath.Join(s.tmpDir, "existing.txt")
	os.WriteFile(p, []byte("x"), 0644)

	cmd := fmt.Sprintf("exists|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] exists: true")
}

func (s *RuleFileIOSuite) TestExistsNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	cmd := fmt.Sprintf("exists|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] exists: false")
}

func (s *RuleFileIOSuite) TestMkdir() {
	d := filepath.Join(s.tmpDir, "newdir")
	cmd := fmt.Sprintf("mkdir|%s", d)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] mkdir: ok")

	info, err := os.Stat(d)
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *RuleFileIOSuite) TestMkdirRecursive() {
	d := filepath.Join(s.tmpDir, "a", "b", "c")
	cmd := fmt.Sprintf("mkdir|%s|recursive", d)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] mkdir: ok")

	info, err := os.Stat(d)
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *RuleFileIOSuite) TestUnlink() {
	p := filepath.Join(s.tmpDir, "todelete.txt")
	os.WriteFile(p, []byte("x"), 0644)

	cmd := fmt.Sprintf("unlink|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] unlink: ok")

	_, err := os.Stat(p)
	s.True(os.IsNotExist(err))
}

func (s *RuleFileIOSuite) TestUnlinkNonExistent() {
	p := filepath.Join(s.tmpDir, "nonexistent.txt")
	cmd := fmt.Sprintf("unlink|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd,
		fmt.Sprintf("[error] fs.unlink() failed: remove %s: no such file or directory", p),
		"[error] caught error")
	s.EnsureGotErrors()
}

func (s *RuleFileIOSuite) TestRename() {
	oldPath := filepath.Join(s.tmpDir, "old.txt")
	newPath := filepath.Join(s.tmpDir, "new.txt")
	os.WriteFile(oldPath, []byte("content"), 0644)

	cmd := fmt.Sprintf("rename|%s|%s", oldPath, newPath)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] rename: ok")

	_, err := os.Stat(oldPath)
	s.True(os.IsNotExist(err))

	data, err := os.ReadFile(newPath)
	s.Require().NoError(err)
	s.Equal("content", string(data))
}

func (s *RuleFileIOSuite) TestWriteFileOverwrite() {
	p := filepath.Join(s.tmpDir, "overwrite.txt")
	cmd := fmt.Sprintf("writeFile|%s|original", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] writeFile: ok")

	cmd = fmt.Sprintf("writeFile|%s|updated", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] writeFile: ok")

	cmd = fmt.Sprintf("readFile|%s", p)
	s.sendCmd(cmd)
	s.verifyLog(cmd, "[info] readFile: updated")
}

func TestRuleFileIOSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleFileIOSuite),
	)
}
