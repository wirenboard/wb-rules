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
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?VGVzdCBzdWJqZWN0?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\nVGVzdCB0ZXh0] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [email send status: ok] (QoS 1)",
	)
}

func (s *RuleNotifyEmailSuite) TestEmailError() {
	s.setErrorCode(1)

	s.publish("/devices/test_email/controls/send/on", "1", "test_email/send")
	s.VerifyUnordered(
		"driver -> /devices/test_email/controls/send: [1] (QoS 1)",
		"tst -> /devices/test_email/controls/send/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending email: Test subject] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: /usr/sbin/sendmail -t] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?VGVzdCBzdWJqZWN0?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\nVGVzdCB0ZXh0] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [email send status: error] (QoS 1)",
	)
}

func (s *RuleNotifyEmailSuite) TestEmailErrorWithoutCallback() {
	s.setErrorCode(1)

	// send_quoted passes no callback, so the error must be logged
	s.publish("/devices/test_email/controls/send_quoted/on", "1", "test_email/send_quoted")
	s.VerifyUnordered(
		"driver -> /devices/test_email/controls/send_quoted: [1] (QoS 1)",
		"tst -> /devices/test_email/controls/send_quoted/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending email: Test \"subject\" 'single'] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: /usr/sbin/sendmail -t] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?VGVzdCAic3ViamVjdCIgJ3NpbmdsZSc=?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\nVGVzdCAidGV4dCIgJ3NpbmdsZSc=] (QoS 1)",
		"wbrules-log -> /wbrules/log/error: [error sending email:\nstdout\nstderr] (QoS 1)",
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
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?VGVzdCAic3ViamVjdCIgJ3NpbmdsZSc=?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\nVGVzdCAidGV4dCIgJ3NpbmdsZSc=] (QoS 1)",
	)
}

// Emoji (and any other character outside the Basic Multilingual Plane) must be
// transmitted as proper UTF-8. Duktape stores strings as CESU-8 internally, so
// the subject/body are base64-encoded from their real UTF-8 bytes rather than
// handed to the shell (or Duktape.enc) directly. Note that the "sending email:"
// log line below still shows the CESU-8 bytes of the emoji — that is a separate
// cosmetic quirk of logging through Duktape, not of the message we send.
func (s *RuleNotifyEmailSuite) TestEmailEmoji() {
	s.setErrorCode(0)

	s.publish("/devices/test_email/controls/send_emoji/on", "1", "test_email/send_emoji")
	s.VerifyUnordered(
		"driver -> /devices/test_email/controls/send_emoji: [1] (QoS 1)",
		"tst -> /devices/test_email/controls/send_emoji/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending email: \xed\xa0\xbc\xed\xbf\xa0 тема] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: /usr/sbin/sendmail -t] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?8J+PoCDRgtC10LzQsA==?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n8J+PoCDRgtC10LrRgdGC] (QoS 1)",
	)
}

// Malformed UTF-16 (a lone surrogate) makes encodeURIComponent throw, which
// would crash sendEmail. Such characters are sanitized to U+FFFD so the message
// still goes out. The "sending email:" log line shows the lone surrogate's
// CESU-8 bytes (ED A0 80), while the actual subject/body carry the sanitized
// U+FFFD (EF BF BD) — Ye+/vWI= / eO+/vXk= in base64.
func (s *RuleNotifyEmailSuite) TestEmailLoneSurrogate() {
	s.setErrorCode(0)

	s.publish("/devices/test_email/controls/send_lone_surrogate/on", "1", "test_email/send_lone_surrogate")
	s.VerifyUnordered(
		"driver -> /devices/test_email/controls/send_lone_surrogate: [1] (QoS 1)",
		"tst -> /devices/test_email/controls/send_lone_surrogate/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending email: a\xed\xa0\x80b] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: /usr/sbin/sendmail -t] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: To: me@example.org\r\nSubject: =?utf-8?B?Ye+/vWI=?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\neO+/vXk=] (QoS 1)",
	)
}

func TestNotifyEmailSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleNotifyEmailSuite),
	)
}
