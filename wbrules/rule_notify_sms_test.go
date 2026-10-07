package wbrules

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleNotifySmsSuite struct {
	RuleSuiteBase
}

func (s *RuleNotifySmsSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_sms_commands.js")
}

func (s *RuleNotifySmsSuite) setErrorCode(seqNum, errorCode int) {
	s.publish(fmt.Sprintf("/devices/test_sms/controls/exit_code_%d/on", seqNum), strconv.Itoa(errorCode),
		fmt.Sprintf("test_sms/exit_code_%d", seqNum))
	s.VerifyUnordered(
		fmt.Sprintf("tst -> /devices/test_sms/controls/exit_code_%d/on: [%d] (QoS 1)", seqNum, errorCode),
		fmt.Sprintf("driver -> /devices/test_sms/controls/exit_code_%d: [%d] (QoS 1, retained)", seqNum, errorCode),
	)
}

func (s *RuleNotifySmsSuite) TestSmsGammu() {
	s.setErrorCode(1, 1) // to make mmcli check OK
	s.setErrorCode(2, 0) // to make gammu happy

	s.publish("/devices/test_sms/controls/send/on", "1", "test_sms/send")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm should_enable] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending sms (gammu-like): test value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm restart_if_broken && gammu sendsms TEXT '88005553535' -unicode] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: test value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sms send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifySmsSuite) TestSmsGammuError() {
	s.setErrorCode(1, 1) // to make mmcli check OK
	s.setErrorCode(2, 1) // to make gammu fail

	s.publish("/devices/test_sms/controls/send/on", "1", "test_sms/send")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm should_enable] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending sms (gammu-like): test value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm restart_if_broken && gammu sendsms TEXT '88005553535' -unicode] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: test value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sms send status: error] (QoS 1)",
	)
}

func (s *RuleNotifySmsSuite) TestSmsErrorWithoutCallback() {
	s.setErrorCode(1, 1) // to make mmcli check OK
	s.setErrorCode(2, 1) // to make gammu fail

	// send_quoted passes no callback, so the error must be logged
	s.publish("/devices/test_sms/controls/send_quoted/on", "1", "test_sms/send_quoted")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send_quoted: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send_quoted/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm should_enable] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending sms (gammu-like): test \"value\" 'single'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm restart_if_broken && gammu sendsms TEXT '88005553535' -unicode] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: test \"value\" 'single'] (QoS 1)",
		"wbrules-log -> /wbrules/log/error: [error sending sms:\nstdout\nstderr] (QoS 1)",
	)
}

const mmcliCommand = `base64 -d | mmcli -m any --messaging-create-sms="number=88005553535"` +
	` --messaging-create-sms-with-text=/dev/stdin -K | cut -d: -f2- | xargs mmcli --send -s`

func (s *RuleNotifySmsSuite) TestSmsModemManager() {
	s.setErrorCode(1, 0) // to make mmcli check OK
	s.setErrorCode(2, 0) // to make mmcli call happy

	s.publish("/devices/test_sms/controls/send/on", "1", "test_sms/send")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm should_enable] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending sms (via ModemManager): test value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: "+mmcliCommand+"] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: dGVzdCB2YWx1ZQ==] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sms send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifySmsSuite) TestSmsModemManagerWithQuotes() {
	s.setErrorCode(1, 0) // to make mmcli check OK
	s.setErrorCode(2, 0) // to make mmcli call happy

	s.publish("/devices/test_sms/controls/send_quoted/on", "1", "test_sms/send_quoted")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send_quoted: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send_quoted/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm should_enable] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending sms (via ModemManager): test \"value\" 'single'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: "+mmcliCommand+"] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: dGVzdCAidmFsdWUiICdzaW5nbGUn] (QoS 1)",
	)
}

func (s *RuleNotifySmsSuite) TestSmsModemManagerTextInjection() {
	s.setErrorCode(1, 0) // to make mmcli check OK
	s.setErrorCode(2, 0) // to make mmcli call happy

	s.publish("/devices/test_sms/controls/send_injection/on", "1", "test_sms/send_injection")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send_injection: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send_injection/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm should_enable] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending sms (via ModemManager): value $(id)] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: "+mmcliCommand+"] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: dmFsdWUgJChpZCk=] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sms send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifySmsSuite) TestSmsModemManagerNonBMP() {
	s.setErrorCode(1, 0) // to make mmcli check OK
	s.setErrorCode(2, 0) // to make mmcli call happy

	s.publish("/devices/test_sms/controls/send_nonbmp/on", "1", "test_sms/send_nonbmp")
	s.SkipTill("wbrules-log -> /wbrules/log/info: [run command: " + mmcliCommand + "] (QoS 1)")
	s.Verify(
		"wbrules-log -> /wbrules/log/info: [input: 8J+PoCDRgtC10LrRgdGC] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sms send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifySmsSuite) TestSmsInvalidRecipient() {
	s.publish("/devices/test_sms/controls/send_bad_number/on", "1", "test_sms/send_bad_number")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send_bad_number: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send_bad_number/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sms send status: error] (QoS 1)",
	)
	s.VerifyEmpty()
}

func TestNotifySmsSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleNotifySmsSuite),
	)
}
