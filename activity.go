package main

import (
	"sync"
	"time"
)

const activityLimit = 300

type activityEvent struct {
	ID       uint64    `json:"id"`
	Time     time.Time `json:"time"`
	Category string    `json:"category"`
	Level    string    `json:"level"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail,omitempty"`
}

// The panel keeps a bounded, process-local journal. It never sends these records
// to Jase, and the public companion endpoints cannot read them.
type activityLog struct {
	mu         sync.Mutex
	next       uint64
	generation uint64
	events     []activityEvent
	contacts   map[string]time.Time
	sent       uint64
	received   uint64
	modelTurns uint64
	errors     uint64
}

func (a *application) record(category, level, title, detail string) {
	l := &a.activity
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recordLocked(category, level, title, detail)
}

// Check the current privacy preference under the journal lock. A model callback
// that began before debug was disabled must not reinsert text after the clear.
func (a *application) recordDetailed(title, detail string) {
	l := &a.activity
	l.mu.Lock()
	defer l.mu.Unlock()
	if a.debug.Load() {
		l.recordLocked("model", "info", title, detail)
	}
}

func (l *activityLog) recordLocked(category, level, title, detail string) {
	l.next++
	if category == "server" {
		l.sent++
	}
	if category == "receive" {
		l.received++
	}
	if category == "model" && title == "Modello avviato" {
		l.modelTurns++
	}
	if level == "error" {
		l.errors++
	}
	e := activityEvent{ID: l.next, Time: time.Now(), Category: category, Level: level, Title: clip(title, 300), Detail: clip(detail, 8000)}
	if len(l.events) >= activityLimit {
		copy(l.events, l.events[1:])
		l.events[len(l.events)-1] = e
	} else {
		l.events = append(l.events, e)
	}
}

func (a *application) serverContact(origin string) {
	l := &a.activity
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.contacts == nil {
		l.contacts = map[string]time.Time{}
	}
	l.contacts[origin] = time.Now()
}

func (a *application) clearActivity() {
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	a.activity.events = nil
	a.activity.generation++
}
