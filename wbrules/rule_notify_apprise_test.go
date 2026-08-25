package wbrules

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleNotifyAppriseSuite struct {
	RuleSuiteBase
}

func (s *RuleNotifyAppriseSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_apprise_commands.js")
}

func (s *RuleNotifyAppriseSuite) setExitCode(exitCode int) {
	s.publish("/devices/test_apprise/controls/exit_code/on", strconv.Itoa(exitCode),
		"test_apprise/exit_code")
	s.VerifyUnordered(
		fmt.Sprintf("tst -> /devices/test_apprise/controls/exit_code/on: [%d] (QoS 1)", exitCode),
		fmt.Sprintf("driver -> /devices/test_apprise/controls/exit_code: [%d] (QoS 1, retained)", exitCode),
	)
}

func (s *RuleNotifyAppriseSuite) TestSendByURL() {
	s.setExitCode(0)

	s.publish("/devices/test_apprise/controls/send_url/on", "1", "test_apprise/send_url")
	s.VerifyUnordered(
		"driver -> /devices/test_apprise/controls/send_url: [1] (QoS 1)",
		"tst -> /devices/test_apprise/controls/send_url/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending apprise notification: plain body] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: base64 -d | apprise -- 'ntfy://ntfy.example/topic'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: cGxhaW4gYm9keQ==] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [apprise send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifyAppriseSuite) TestSendByTag() {
	s.setExitCode(0)

	s.publish("/devices/test_apprise/controls/send_tag/on", "1", "test_apprise/send_tag")
	s.VerifyUnordered(
		"driver -> /devices/test_apprise/controls/send_tag: [1] (QoS 1)",
		"tst -> /devices/test_apprise/controls/send_tag/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending apprise notification: tagged body] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: base64 -d | apprise -g 'alarm'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: dGFnZ2VkIGJvZHk=] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [apprise send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifyAppriseSuite) TestSendWithTitle() {
	s.setExitCode(0)

	// 0KLRgNC10LLQvtCz0LA= is base64("Тревога"), 0J3QsNGB0L7RgQ== is base64("Насос")
	s.publish("/devices/test_apprise/controls/send_titled/on", "1", "test_apprise/send_titled")
	s.VerifyUnordered(
		"driver -> /devices/test_apprise/controls/send_titled: [1] (QoS 1)",
		"tst -> /devices/test_apprise/controls/send_titled/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending apprise notification: Насос] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: base64 -d | apprise -t \"$(printf %s '0KLRgNC10LLQvtCz0LA=' | base64 -d)\" -- 'ntfy://ntfy.example/topic'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: 0J3QsNGB0L7RgQ==] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [apprise send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifyAppriseSuite) TestSendError() {
	s.setExitCode(1)

	s.publish("/devices/test_apprise/controls/send_url/on", "1", "test_apprise/send_url")
	s.VerifyUnordered(
		"driver -> /devices/test_apprise/controls/send_url: [1] (QoS 1)",
		"tst -> /devices/test_apprise/controls/send_url/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending apprise notification: plain body] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: base64 -d | apprise -- 'ntfy://ntfy.example/topic'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: cGxhaW4gYm9keQ==] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [apprise send status: error] (QoS 1)",
	)
}

func (s *RuleNotifyAppriseSuite) TestNotInstalledWithoutCallback() {
	// exit code 127 means the shell could not find the apprise binary;
	// without a callback the module must log a human-readable hint
	s.setExitCode(127)

	s.publish("/devices/test_apprise/controls/send_no_callback/on", "1", "test_apprise/send_no_callback")
	s.VerifyUnordered(
		"driver -> /devices/test_apprise/controls/send_no_callback: [1] (QoS 1)",
		"tst -> /devices/test_apprise/controls/send_no_callback/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending apprise notification: no callback body] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: base64 -d | apprise -- 'ntfy://ntfy.example/topic'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: bm8gY2FsbGJhY2sgYm9keQ==] (QoS 1)",
		"wbrules-log -> /wbrules/log/error: [error sending apprise notification: the 'apprise' package is not installed; run 'apt-get install apprise' (available since Debian 13)] (QoS 1)",
	)
}

func (s *RuleNotifyAppriseSuite) TestEmptyTarget() {
	s.publish("/devices/test_apprise/controls/send_empty_target/on", "1", "test_apprise/send_empty_target")
	s.VerifyUnordered(
		"driver -> /devices/test_apprise/controls/send_empty_target: [1] (QoS 1)",
		"tst -> /devices/test_apprise/controls/send_empty_target/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [apprise send status: error] (QoS 1)",
	)
}

func TestNotifyAppriseSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleNotifyAppriseSuite),
	)
}
