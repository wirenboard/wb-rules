package wbrules

import (
	"github.com/contactless/wbgo/testutils"
	"os"
	"path"
	"testing"
)

type RuleShellCommandSuite struct {
	RuleSuiteBase
}

func (s *RuleShellCommandSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_command.js")
}

func (s *RuleShellCommandSuite) fileExists(path string) bool {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false
		} else {
			s.Require().Fail("unexpected error when checking for samplefile", "%s", err)
		}
	}
	return true
}

func (s *RuleShellCommandSuite) verifyFileExists(path string) {
	if !s.fileExists(path) {
		s.Require().Fail("file does not exist", "%s", path)
	}
}

func (s *RuleShellCommandSuite) TestRunShellCommand() {
	dir, cleanup := testutils.SetupTempDir(s.T())
	defer cleanup()

	s.publish("/devices/somedev/controls/cmd/meta/type", "text", "somedev/cmd")
	s.publish("/devices/somedev/controls/cmdNoCallback/meta/type", "text",
		"somedev/cmdNoCallback")
	s.publish("/devices/somedev/controls/cmd", "touch samplefile.txt", "somedev/cmd")
	s.Verify(
		"tst -> /devices/somedev/controls/cmd/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmdNoCallback/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmd: [touch samplefile.txt] (QoS 1, retained)",
		"[info] cmd: touch samplefile.txt",
		"[info] exit(0): touch samplefile.txt",
	)

	s.verifyFileExists(path.Join(dir, "samplefile.txt"))

	s.publish("/devices/somedev/controls/cmd", "touch nosuchdir/samplefile.txt 2>/dev/null", "somedev/cmd")
	s.Verify(
		"tst -> /devices/somedev/controls/cmd: [touch nosuchdir/samplefile.txt 2>/dev/null] (QoS 1, retained)",
		"[info] cmd: touch nosuchdir/samplefile.txt 2>/dev/null",
		"[info] exit(1): touch nosuchdir/samplefile.txt 2>/dev/null", // no such file or directory
	)

	s.publish("/devices/somedev/controls/cmdNoCallback", "1", "somedev/cmdNoCallback")
	s.publish("/devices/somedev/controls/cmd", "touch samplefile1.txt", "somedev/cmd")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdNoCallback: [1] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmd: [touch samplefile1.txt] (QoS 1, retained)",
		"[info] cmd: touch samplefile1.txt",
		"[info] (no callback)",
	)
	s.WaitFor(func() bool {
		return s.fileExists(path.Join(dir, "samplefile1.txt"))
	})
}

func (s *RuleShellCommandSuite) TestRunShellCommandIO() {
	s.publish("/devices/somedev/controls/cmdWithOutput/meta/type", "text",
		"somedev/cmdWithOutput")
	s.publish("/devices/somedev/controls/cmdWithOutput", "echo abc; echo qqq",
		"somedev/cmdWithOutput")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput/meta/type: [text] (QoS 1, retained)",
		"tst -> /devices/somedev/controls/cmdWithOutput: [echo abc; echo qqq] (QoS 1, retained)",
		"[info] cmdWithOutput: echo abc; echo qqq",
		"[info] exit(0): echo abc; echo qqq",
		"[info] output: abc",
		"[info] output: qqq",
	)

	s.publish("/devices/somedev/controls/cmdWithOutput", "echo abc; echo qqq 1>&2; exit 1",
		"somedev/cmdWithOutput")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput: [echo abc; echo qqq 1>&2; exit 1] (QoS 1, retained)",
		"[info] cmdWithOutput: echo abc; echo qqq 1>&2; exit 1",
		"[info] exit(1): echo abc; echo qqq 1>&2; exit 1",
		"[info] output: abc",
		"[info] error: qqq",
	)

	s.publish("/devices/somedev/controls/cmdWithOutput", "xxyz!sed s/x/y/g",
		"somedev/cmdWithOutput")
	s.Verify(
		"tst -> /devices/somedev/controls/cmdWithOutput: [xxyz!sed s/x/y/g] (QoS 1, retained)",
		"[info] cmdWithOutput: sed s/x/y/g",
		"[info] exit(0): sed s/x/y/g",
		"[info] output: yyyz",
	)
}

func TestRuleShellCommandSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleShellCommandSuite),
	)
}
