package wbrules

import (
	"sync"
)

const (
	EVENT_BUFFER_CAP    = 16
	EVENT_OBSERVERS_CAP = 1
)

type EventBuffer struct {
	sync.Mutex

	currentBuffer []*ControlChangeEvent
	observer      chan struct{}
}

func NewEventBuffer() *EventBuffer {
	return &EventBuffer{
		currentBuffer: make([]*ControlChangeEvent, 0, EVENT_BUFFER_CAP),
		observer:      make(chan struct{}, 1),
	}
}

func (eb *EventBuffer) Observe() <-chan struct{} {
	return eb.observer
}

func (eb *EventBuffer) PushEvent(e *ControlChangeEvent) {
	eb.Lock()
	defer eb.Unlock()

	eb.currentBuffer = append(eb.currentBuffer, e)

	// try to notify user if he's not notified already
	select {
	case eb.observer <- struct{}{}:
	default:
	}
}

func (eb *EventBuffer) Retrieve() (e []*ControlChangeEvent) {
	eb.Lock()
	defer eb.Unlock()

	e = eb.currentBuffer
	eb.currentBuffer = make([]*ControlChangeEvent, 0, EVENT_BUFFER_CAP)
	return
}

func (eb *EventBuffer) length() int {
	eb.Lock()
	defer eb.Unlock()

	return len(eb.currentBuffer)
}

func (eb *EventBuffer) Close() {
	close(eb.observer)
}
