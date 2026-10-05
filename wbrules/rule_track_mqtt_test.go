package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleTrackMqttSuite struct {
	RuleSuiteBase
}

func (s *RuleTrackMqttSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_track_mqtt.js")
}

// TestTracker tests js which contains tracking like this:
//
// trackMqtt("/weird/sub/some", ...
// trackMqtt("/weird/+/some", ...
// trackMqtt("/weird/+/another", ...
// trackMqtt("/weird/#", ...
func (s *RuleTrackMqttSuite) TestTracker() {
	s.publish("/weird/sub/some", "some-value")
	s.VerifyUnordered(
		"tst -> /weird/sub/some: [some-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [1. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/sub/some, value: some-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [2. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/sub/some, value: some-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/sub/some, value: some-value, retained: true, qos: 1] (QoS 1)",
	)

	s.publish("/weird/sub2/some", "some-value")
	s.VerifyUnordered(
		"tst -> /weird/sub2/some: [some-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [2. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/sub2/some, value: some-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/sub2/some, value: some-value, retained: true, qos: 1] (QoS 1)",
	)

	s.publish("/weird/sub3/another", "another-value")
	s.VerifyUnordered(
		"tst -> /weird/sub3/another: [another-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [3. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/sub3/another, value: another-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/sub3/another, value: another-value, retained: true, qos: 1] (QoS 1)",
	)

	s.publish("/weird/different/long/topic/on", "random-value")
	s.VerifyUnordered(
		"tst -> /weird/different/long/topic/on: [random-value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. weird topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /weird/different/long/topic/on, value: random-value, retained: false, qos: 1] (QoS 1)",
	)

	s.VerifyEmpty()
}

func TestTrackMqtt(t *testing.T) {
	testutils.RunSuites(t, new(RuleTrackMqttSuite))
}
