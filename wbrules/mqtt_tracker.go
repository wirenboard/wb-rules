package wbrules

// TrackID is used to watch a track in cleanups
type MqttTrackerID uint32

type MqttTrackerMap map[MqttTrackerID]MqttTracker

type MqttTracker struct {
	ID       MqttTrackerID
	Topic    string
	Callback ESCallbackFunc
}

// NewMqttTracker returns new mqtt tracker instance
func NewMqttTracker(topic string, id MqttTrackerID) MqttTracker {
	return MqttTracker{
		ID:    id,
		Topic: topic,
	}
}
