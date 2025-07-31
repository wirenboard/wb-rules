package wbrules

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleNotifyEmailSuite struct {
	RuleSuiteBase
}

func (s *RuleNotifyEmailSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_email_commands.js")
}

func (s *RuleNotifyEmailSuite) setErrorCode(errorCode int) {
	s.publish("/devices/test_email/controls/exit_code/on", strconv.Itoa(errorCode),
		"test_email/exit_code")
	s.VerifyUnordered(
		fmt.Sprintf("tst -> /devices/test_email/controls/exit_code/on: [%d] (QoS 1)", errorCode),
		fmt.Sprintf("driver -> /devices/test_email/controls/exit_code: [%d] (QoS 1, retained)", errorCode),
	)
}

func (s *RuleNotifyEmailSuite) TestEmail() {
	s.setErrorCode(0)

	s.publish("/devices/test_email/controls/send/on", "1", "test_email/send")
	s.VerifyUnordered(
		"driver -> /devices/test_email/controls/send: [1] (QoS 1)",
		"tst -> /devices/test_email/controls/send/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending email: Test subject] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: /usr/sbin/sendmail -t] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?VGVzdCBzdWJqZWN0?=\r\nContent-Type: text/plain; charset=utf-8\n\nTest text] (QoS 1)",
	)
}

func (s *RuleNotifyEmailSuite) TestEmailWithQuotes() {
	s.setErrorCode(0)

	s.publish("/devices/test_email/controls/send_quoted/on", "1", "test_email/send_quoted")
	s.VerifyUnordered(
		"driver -> /devices/test_email/controls/send_quoted: [1] (QoS 1)",
		"tst -> /devices/test_email/controls/send_quoted/on: [1] (QoS 1)",
        "wbrules-log -> /wbrules/log/info: [sending email: Test \"subject\" 'single'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: /usr/sbin/sendmail -t] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?VGVzdCAic3ViamVjdCIgJ3NpbmdsZSc=?=\r\nContent-Type: text/plain; charset=utf-8\n\nTest \"text\" 'single'] (QoS 1)",
	)
}

func TestNotifyEmailSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleNotifyEmailSuite),
	)
}
