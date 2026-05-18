package wbrules

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleNotifyWebhookSuite struct {
	RuleSuiteBase
}

func (s *RuleNotifyWebhookSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_webhook_commands.js")
}

func (s *RuleNotifyWebhookSuite) setErrorCode(errorCode int) {
	s.publish("/devices/test_webhook/controls/exit_code/on", strconv.Itoa(errorCode),
		"test_webhook/exit_code")
	s.VerifyUnordered(
		fmt.Sprintf("tst -> /devices/test_webhook/controls/exit_code/on: [%d] (QoS 1)", errorCode),
		fmt.Sprintf("driver -> /devices/test_webhook/controls/exit_code: [%d] (QoS 1, retained)", errorCode),
	)
}

func (s *RuleNotifyWebhookSuite) TestPlainTextBody() {
	s.setErrorCode(0)

	s.publish("/devices/test_webhook/controls/send_text/on", "1", "test_webhook/send_text")
	s.VerifyUnordered(
		"driver -> /devices/test_webhook/controls/send_text: [1] (QoS 1)",
		"tst -> /devices/test_webhook/controls/send_text/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending webhook: POST https://example.com/hook] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -sS --fail -X'POST' 'https://example.com/hook' -H 'Content-Type: text/plain; charset=utf-8' --data-binary @-] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: plain text body] (QoS 1)",
	)
}

func (s *RuleNotifyWebhookSuite) TestObjectBodyAsJSON() {
	s.setErrorCode(0)

	s.publish("/devices/test_webhook/controls/send_json_object/on", "1", "test_webhook/send_json_object")
	s.VerifyUnordered(
		"driver -> /devices/test_webhook/controls/send_json_object: [1] (QoS 1)",
		"tst -> /devices/test_webhook/controls/send_json_object/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending webhook: POST https://example.com/hook] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -sS --fail -X'POST' 'https://example.com/hook' -H 'Content-Type: application/json' --data-binary @-] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: {\"event\":\"alarm\",\"value\":42}] (QoS 1)",
	)
}

func (s *RuleNotifyWebhookSuite) TestJSONStringDetected() {
	s.setErrorCode(0)

	s.publish("/devices/test_webhook/controls/send_json_string/on", "1", "test_webhook/send_json_string")
	s.VerifyUnordered(
		"driver -> /devices/test_webhook/controls/send_json_string: [1] (QoS 1)",
		"tst -> /devices/test_webhook/controls/send_json_string/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending webhook: POST https://example.com/hook] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -sS --fail -X'POST' 'https://example.com/hook' -H 'Content-Type: application/json' --data-binary @-] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: {\"alert\":\"text\"}] (QoS 1)",
	)
}

func (s *RuleNotifyWebhookSuite) TestGetNoBody() {
	s.setErrorCode(0)

	s.publish("/devices/test_webhook/controls/send_get_no_body/on", "1", "test_webhook/send_get_no_body")
	s.VerifyUnordered(
		"driver -> /devices/test_webhook/controls/send_get_no_body: [1] (QoS 1)",
		"tst -> /devices/test_webhook/controls/send_get_no_body/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending webhook: GET https://example.com/ping] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -sS --fail -X'GET' 'https://example.com/ping' -H 'Content-Type: text/plain; charset=utf-8'] (QoS 1)",
	)
}

func (s *RuleNotifyWebhookSuite) TestCustomHeaders() {
	s.setErrorCode(0)

	s.publish("/devices/test_webhook/controls/send_with_headers/on", "1", "test_webhook/send_with_headers")
	s.VerifyUnordered(
		"driver -> /devices/test_webhook/controls/send_with_headers: [1] (QoS 1)",
		"tst -> /devices/test_webhook/controls/send_with_headers/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending webhook: POST https://example.com/hook] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -sS --fail -X'POST' 'https://example.com/hook' -H 'Content-Type: application/json' -H 'Authorization: Bearer xyz' --data-binary @-] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: {\"ok\":true}] (QoS 1)",
	)
}

func (s *RuleNotifyWebhookSuite) TestCustomContentType() {
	s.setErrorCode(0)

	s.publish("/devices/test_webhook/controls/send_custom_ct/on", "1", "test_webhook/send_custom_ct")
	s.VerifyUnordered(
		"driver -> /devices/test_webhook/controls/send_custom_ct: [1] (QoS 1)",
		"tst -> /devices/test_webhook/controls/send_custom_ct/on: [1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [sending webhook: POST https://example.com/hook] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [run command: curl -sS --fail -X'POST' 'https://example.com/hook' -H 'Content-Type: text/csv; charset=utf-8' --data-binary @-] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [input: event,value\nalarm,42] (QoS 1)",
	)
}

func TestNotifyWebhookSuite(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleNotifyWebhookSuite),
	)
}
