package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// RuleTrackMqttReloadSuite covers reloading one of several scripts that track
// the same MQTT topic. Two scripts (dup1, dup2) both call
// trackMqtt("/wierd/sub/some", ...). The subscription is shared between them,
// so reloading one script does not tear the subscription down. Previously the
// reloaded tracker never received the current (retained) value because the
// broker only redelivers retained values on a fresh subscription. Now the
// cached retained value is replayed to the reloaded tracker.
type RuleTrackMqttReloadSuite struct {
	RuleSuiteBase
}

func (s *RuleTrackMqttReloadSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_track_mqtt_dup1.js", "testrules_track_mqtt_dup2.js")
}

func (s *RuleTrackMqttReloadSuite) TestReloadWithSharedTopic() {
	// A retained value reaches both trackers.
	s.publish("/wierd/sub/some", "some-value")
	s.VerifyUnordered(
		"tst -> /wierd/sub/some: [some-value] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [tmp1: /wierd/sub/some=some-value (retained: true)] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [tmp2: /wierd/sub/some=some-value (retained: true)] (QoS 1)",
	)

	// Reloading dup1 (dup2 keeps the shared subscription alive) must replay the
	// cached retained value to the reloaded tracker only. dup2 stays silent.
	s.ReplaceScript("testrules_track_mqtt_dup1.js", "testrules_track_mqtt_dup1.js")
	s.VerifyUnordered(
		"wbrules-log -> /wbrules/updates/changed: [testrules_track_mqtt_dup1.js] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [tmp1: /wierd/sub/some=some-value (retained: true)] (QoS 1)",
	)

	s.VerifyEmpty()
}

func TestTrackMqttReload(t *testing.T) {
	testutils.RunSuites(t, new(RuleTrackMqttReloadSuite))
}
