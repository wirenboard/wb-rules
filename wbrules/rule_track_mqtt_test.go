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
// trackMqtt("/wierd/sub/some", ...
// trackMqtt("/wierd/+/some", ...
// trackMqtt("/wierd/+/another", ...
// trackMqtt("/wierd/#", ...
func (s *RuleTrackMqttSuite) TestTracker() {
	s.publish("/wierd/sub/some", "some-value")
	s.VerifyUnordered(
		"tst -> /wierd/sub/some: [some-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [1. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub/some, value: some-value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [2. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub/some, value: some-value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub/some, value: some-value] (QoS 1)",
	)

	s.publish("/wierd/sub2/some", "some-value")
	s.VerifyUnordered(
		"tst -> /wierd/sub2/some: [some-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [2. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub2/some, value: some-value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub2/some, value: some-value] (QoS 1)",
	)

	s.publish("/wierd/sub3/another", "another-value")
	s.VerifyUnordered(
		"tst -> /wierd/sub3/another: [another-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [3. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub3/another, value: another-value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub3/another, value: another-value] (QoS 1)",
	)

	s.publish("/wierd/different/long/topic", "random-value")
	s.VerifyUnordered(
		"tst -> /wierd/different/long/topic: [random-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/different/long/topic, value: random-value] (QoS 1)",
	)

	s.VerifyEmpty()
}

func TestTrackMqtt(t *testing.T) {
	testutils.RunSuites(t, new(RuleTrackMqttSuite))
}
