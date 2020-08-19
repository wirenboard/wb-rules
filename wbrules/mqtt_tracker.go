package wbrules

type MqttTrackerMap map[uint32]MqttTracker

type MqttTracker struct {
	ID       uint32
	Topic    string
	Callback ESCallbackFunc
}

// NewMqttTracker returns new mqtt tracker instance
func NewMqttTracker(topic string, id uint32) MqttTracker {
	return MqttTracker{
		ID:    id,
		Topic: topic,
	}
}
