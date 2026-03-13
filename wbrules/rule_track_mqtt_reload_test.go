package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

// RuleTrackMqttReloadSuite tests that reloading a script with trackMqtt
// doesn't cause panic ("operation on invalid context").
//
// Reproduces SOFT-5628: two scripts track the same MQTT topic; reloading
// one of them while messages are delivered causes invokeCallback to run
// on an already-invalidated ESContext.
type RuleTrackMqttReloadSuite struct {
	RuleSuiteBase
}

func (s *RuleTrackMqttReloadSuite) SetupTest() {
	s.SetupSkippingDefs(
		"testrules_track_mqtt_reload_1.js",
		"testrules_track_mqtt_reload_2.js",
	)
}

// TestTrackMqttBeforeReload verifies both trackers fire before any reload.
func (s *RuleTrackMqttReloadSuite) TestTrackMqttBeforeReload() {
	s.publish("/tracker/topic", "hello")
	s.VerifyUnordered(
		"tst -> /tracker/topic: [hello] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [tracker1: topic=/tracker/topic, value=hello] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [tracker2: topic=/tracker/topic, value=hello] (QoS 1)",
	)
	s.VerifyEmpty()
}

// TestTrackMqttReloadScript reloads script 2 and verifies:
// - no panic occurs
// - the old tracker from script 2 is cleaned up
// - the new tracker from script 2 fires with updated callback
// - tracker from script 1 still works
func (s *RuleTrackMqttReloadSuite) TestTrackMqttReloadScript() {
	// Verify both trackers work initially
	s.publish("/tracker/topic", "before")
	s.VerifyUnordered(
		"tst -> /tracker/topic: [before] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [tracker1: topic=/tracker/topic, value=before] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [tracker2: topic=/tracker/topic, value=before] (QoS 1)",
	)

	// Reload script 2 with a changed version
	s.ReplaceScript("testrules_track_mqtt_reload_2.js", "testrules_track_mqtt_reload_2_changed.js")
	s.VerifyUnordered(
		"wbrules-log -> /wbrules/updates/changed: [testrules_track_mqtt_reload_2.js] (QoS 1)",
	)

	// Publish again — tracker1 should still fire, tracker2_v2 should fire (not tracker2)
	s.publish("/tracker/topic", "after")
	s.VerifyUnordered(
		"tst -> /tracker/topic: [after] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [tracker1: topic=/tracker/topic, value=after] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [tracker2_v2: topic=/tracker/topic, value=after] (QoS 1)",
	)
	s.VerifyEmpty()
}

// TestTrackMqttRemoveScript removes script 2 entirely and verifies
// that tracker from script 1 still works and no panic occurs.
func (s *RuleTrackMqttReloadSuite) TestTrackMqttRemoveScript() {
	s.publish("/tracker/topic", "before")
	s.VerifyUnordered(
		"tst -> /tracker/topic: [before] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [tracker1: topic=/tracker/topic, value=before] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [tracker2: topic=/tracker/topic, value=before] (QoS 1)",
	)

	// Remove script 2
	s.RemoveScript("testrules_track_mqtt_reload_2.js")
	s.VerifyUnordered(
		"wbrules-log -> /wbrules/updates/removed: [testrules_track_mqtt_reload_2.js] (QoS 1)",
	)

	// Only tracker1 should fire now
	s.publish("/tracker/topic", "after")
	s.VerifyUnordered(
		"tst -> /tracker/topic: [after] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [tracker1: topic=/tracker/topic, value=after] (QoS 1)",
	)
	s.VerifyEmpty()
}

func TestTrackMqttReload(t *testing.T) {
	testutils.RunSuites(t, new(RuleTrackMqttReloadSuite))
}
