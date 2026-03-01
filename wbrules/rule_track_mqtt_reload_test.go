package wbrules

import (
	"testing"

	"github.com/wirenboard/wbgong/testutils"
)

type RuleTrackMqttReloadSuite struct {
	RuleSuiteBase
}

func (s *RuleTrackMqttReloadSuite) SetupTest() {
	s.SetupSkippingDefs(
		"testrules_track_mqtt_reload_1.js",
		"testrules_track_mqtt_reload_2.js",
	)
}

// TestTrackMqttCallbackAfterReload verifies that reloading a script
// with trackMqtt does not panic when messages arrive on the tracked topic.
// This is a regression test for SOFT-5628: "panic: operation on invalid context"
// caused by an MQTT callback being queued on the sync loop before the
// script's ESContext is invalidated during reload, then executing after.
func (s *RuleTrackMqttReloadSuite) TestTrackMqttCallbackAfterReload() {
	// Verify initial trackMqtt works
	s.publish("/test/reload", "before_reload")
	s.VerifyUnordered(
		"tst -> /test/reload: [before_reload] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [script1: topic=/test/reload, value=before_reload] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [script2: topic=/test/reload, value=before_reload] (QoS 1)",
	)

	// Reload script1 with new content — this invalidates the old ESContext.
	// If a trackMqtt callback from the old context is still queued,
	// invoking it used to panic with "operation on invalid context".
	s.ReplaceScript("testrules_track_mqtt_reload_1.js", "testrules_track_mqtt_reload_1_v2.js")
	s.SkipTill("[changed] testrules_track_mqtt_reload_1.js")

	// Publish again. Should use the new script1 callback and the
	// original script2 callback, with no panic.
	s.publish("/test/reload", "after_reload")
	s.VerifyUnordered(
		"tst -> /test/reload: [after_reload] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [script1_v2: topic=/test/reload, value=after_reload] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [script2: topic=/test/reload, value=after_reload] (QoS 1)",
	)
}

// TestTrackMqttRepeatedReloads verifies that rapid repeated reloads
// of scripts with trackMqtt do not panic.
func (s *RuleTrackMqttReloadSuite) TestTrackMqttRepeatedReloads() {
	for i := 0; i < 5; i++ {
		s.publish("/test/reload", "msg")
		s.SkipTill("tst -> /test/reload: [msg] (QoS 1, retained)")

		if i%2 == 0 {
			s.ReplaceScript("testrules_track_mqtt_reload_1.js", "testrules_track_mqtt_reload_1_v2.js")
		} else {
			s.ReplaceScript("testrules_track_mqtt_reload_1.js", "testrules_track_mqtt_reload_1.js")
		}
		s.SkipTill("[changed] testrules_track_mqtt_reload_1.js")
	}

	// Final publish to verify everything still works.
	// After 5 iterations (0..4), the last reload (i=4, even) uses v2.
	s.publish("/test/reload", "final")
	s.VerifyUnordered(
		"tst -> /test/reload: [final] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [script1_v2: topic=/test/reload, value=final] (QoS 1)",
		"wbrules-log -> /wbrules/log/info: [script2: topic=/test/reload, value=final] (QoS 1)",
	)
}

// RuleTrackMqttSoloReloadSuite tests reload of the sole script tracking a topic.
// When a topic has only one tracker and the script is reloaded, cleanup removes
// the last tracker entry. The outer map key must also be deleted so that the
// reloaded script's DefineMqttTracker re-subscribes to MQTT.
type RuleTrackMqttSoloReloadSuite struct {
	RuleSuiteBase
}

func (s *RuleTrackMqttSoloReloadSuite) SetupTest() {
	s.SetupSkippingDefs("testrules_track_mqtt_solo.js")
}

func (s *RuleTrackMqttSoloReloadSuite) TestSoleTrackerResubscribesAfterReload() {
	// Verify initial trackMqtt works
	s.publish("/test/solo", "v1")
	s.VerifyUnordered(
		"tst -> /test/solo: [v1] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [solo: topic=/test/solo, value=v1] (QoS 1)",
	)

	// Reload with new content. The old tracker is the ONLY one for /test/solo,
	// so cleanup should fully remove the topic from engine.tracks and
	// unsubscribe. The reloaded script must re-subscribe.
	s.ReplaceScript("testrules_track_mqtt_solo.js", "testrules_track_mqtt_solo_v2.js")
	s.SkipTill("[changed] testrules_track_mqtt_solo.js")

	// The re-subscribe delivers the retained "v1" message to the new handler.
	// Drain it before verifying the next publish.
	s.SkipTill("[info] solo_v2: topic=/test/solo, value=v1")

	// This publish will fail silently if re-subscribe was skipped
	// (the stale empty map bug).
	s.publish("/test/solo", "v2")
	s.VerifyUnordered(
		"tst -> /test/solo: [v2] (QoS 1, retained)",
		"wbrules-log -> /wbrules/log/info: [solo_v2: topic=/test/solo, value=v2] (QoS 1)",
	)
}

func TestTrackMqttReload(t *testing.T) {
	testutils.RunSuites(t,
		new(RuleTrackMqttReloadSuite),
		new(RuleTrackMqttSoloReloadSuite),
	)
}
