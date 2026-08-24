package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong"
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
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub/some, value: some-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [2. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub/some, value: some-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub/some, value: some-value, retained: true, qos: 1] (QoS 1)",
	)

	s.publish("/wierd/sub2/some", "some-value")
	s.VerifyUnordered(
		"tst -> /wierd/sub2/some: [some-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [2. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub2/some, value: some-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub2/some, value: some-value, retained: true, qos: 1] (QoS 1)",
	)

	s.publish("/wierd/sub3/another", "another-value")
	s.VerifyUnordered(
		"tst -> /wierd/sub3/another: [another-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [3. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub3/another, value: another-value, retained: true, qos: 1] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/sub3/another, value: another-value, retained: true, qos: 1] (QoS 1)",
	)

	s.publish("/wierd/different/long/topic/on", "random-value")
	s.VerifyUnordered(
		"tst -> /wierd/different/long/topic/on: [random-value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [4. wierd topic got value] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [topic: /wierd/different/long/topic/on, value: random-value, retained: false, qos: 1] (QoS 1)",
	)

	s.VerifyEmpty()
}

// trackMqtt(topic, cb, {cache: false}): the subscription keeps no
// last-value cache, so a tracker joining the pattern later gets nothing
// replayed (request/reply streams: one-off messages, unbounded topics).
type RuleTrackMqttNoCacheSuite struct {
	RuleSuiteBase
}

func (s *RuleTrackMqttNoCacheSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_track_mqtt_nocache.js")
}

func (s *RuleTrackMqttNoCacheSuite) TestNoReplayForLateJoiner() {
	s.client.Publish(wbgong.MQTTMessage{Topic: "/nocache/one", Payload: "first", QoS: 1})
	s.Verify(
		"tst -> /nocache/one: [first] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [nocache A: /nocache/one = first retained=false] (QoS 1)",
	)
	// a second file joins the same pattern: with the default cache it would
	// be handed "first" as a retained replay; without one it hears only
	// what comes next
	s.Ck("LiveLoadScript", s.LiveLoadScript("testrules_track_mqtt_nocache2.js"))
	s.SkipTill("wbrules-log -> /wbrules/updates/changed: [testrules_track_mqtt_nocache2.js] (QoS 1)")
	s.VerifyEmpty()
	s.client.Publish(wbgong.MQTTMessage{Topic: "/nocache/one", Payload: "second", QoS: 1})
	s.VerifyUnordered(
		"tst -> /nocache/one: [second] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [nocache A: /nocache/one = second retained=false] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [nocache B: /nocache/one = second retained=false] (QoS 1)",
	)
}

func TestTrackMqtt(t *testing.T) {
	testutils.RunSuites(t, new(RuleTrackMqttSuite), new(RuleTrackMqttNoCacheSuite))
}
