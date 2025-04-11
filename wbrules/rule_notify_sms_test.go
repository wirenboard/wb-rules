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
		"wbrules-log -> /wbrules/log/info: [sending sms (gammu-like) to 88005553535: test value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm restart_if_broken && gammu sendsms TEXT '88005553535' -unicode] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: test value] (QoS 1)",
	)
}

func (s *RuleNotifySmsSuite) TestSmsModemManager() {
	s.setErrorCode(1, 0) // to make mmcli check OK
	s.setErrorCode(2, 0) // to make mmcli call happy

	s.publish("/devices/test_sms/controls/send/on", "1", "test_sms/send")
	s.VerifyUnordered(
		"driver -> /devices/test_sms/controls/send: [1] (QoS 1)",
		"tst -> /devices/test_sms/controls/send/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: wb-gsm should_enable] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending sms (via ModemManager) to 88005553535: test value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: mmcli -m any --messaging-create-sms=\"number=88005553535,text=\\\"test value\\\"\" | sed -n 's#^Success.*/SMS/\\([0-9]\\+\\).*$#\\1#p' | xargs mmcli --send -s] (QoS 1)",
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
		"wbrules-log -> /wbrules/log/info: [sending sms (via ModemManager) to 88005553535: test \"value\" 'single'] (QoS 1)",
		"wbrules-log -> /wbrules/log/warning: [ModemManager can't handle SMS with double quotes now, auto replaced with single ones] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: mmcli -m any --messaging-create-sms=\"number=88005553535,text=\\\"test 'value' 'single'\\\"\" | sed -n 's#^Success.*/SMS/\\([0-9]\\+\\).*$#\\1#p' | xargs mmcli --send -s] (QoS 1)",
	)
}

func TestNotifySmsSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleNotifySmsSuite),
	)
}
