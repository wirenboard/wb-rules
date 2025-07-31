package wbrules

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleNotifyTgSuite struct {
	RuleSuiteBase
}

func (s *RuleNotifyTgSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_tg_commands.js")
}

func (s *RuleNotifyTgSuite) setErrorCode(errorCode int) {
	s.publish("/devices/test_tg/controls/exit_code/on", strconv.Itoa(errorCode),
		"test_tg/exit_code")
	s.VerifyUnordered(
		fmt.Sprintf("tst -> /devices/test_tg/controls/exit_code/on: [%d] (QoS 1)", errorCode),
		fmt.Sprintf("driver -> /devices/test_tg/controls/exit_code: [%d] (QoS 1, retained)", errorCode),
	)
}

func (s *RuleNotifyTgSuite) TestTg() {
	s.setErrorCode(0)

	s.publish("/devices/test_tg/controls/send/on", "1", "test_tg/send")
	s.VerifyUnordered(
		"driver -> /devices/test_tg/controls/send: [1] (QoS 1)",
		"tst -> /devices/test_tg/controls/send/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending telegram message: Test message] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -s -X POST https://api.telegram.org/bot1234567890:abcdefghijklmnopqrstuvwxyz123456789/sendMessage -H 'Content-Type: application/x-www-form-urlencoded' -d @-] (QoS 1)",
        "wbrules-log -> /wbrules/log/info: [input: chat_id=12345678&text=Test%20message] (QoS 1)",
	)
}

func (s *RuleNotifyTgSuite) TestTgWithQuotes() {
	s.setErrorCode(0)

	s.publish("/devices/test_tg/controls/send_quoted/on", "1", "test_tg/send_quoted")
	s.VerifyUnordered(
		"driver -> /devices/test_tg/controls/send_quoted: [1] (QoS 1)",
		"tst -> /devices/test_tg/controls/send_quoted/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending telegram message: Test \"message\" 'single'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -s -X POST https://api.telegram.org/bot1234567890:abcdefghijklmnopqrstuvwxyz123456789/sendMessage -H 'Content-Type: application/x-www-form-urlencoded' -d @-] (QoS 1)",
        "wbrules-log -> /wbrules/log/info: [input: chat_id=12345678&text=Test%20%22message%22%20'single'] (QoS 1)",
	)
}

func TestNotifyTgSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleNotifyTgSuite),
	)
}
