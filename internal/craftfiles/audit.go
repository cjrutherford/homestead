package craftfiles

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Event is one audited file operation: metadata only, never content,
// credentials, cookies or launch codes.
type Event struct {
	Operator string
	Session  string
	App      string
	Method   string
	Path     string // normalised, relative to the root folder
	Status   int
	Bytes    int64
	// Count is how many reads an aggregated entry stands for; 1 otherwise.
	Count int
}

// IsRead reports the methods that only read.
func IsRead(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, "PROPFIND":
		return true
	}
	return false
}

type readKey struct {
	session, method, path string
	minute                int64
}

// Auditor records writes immediately and aggregates reads per session,
// path and minute so a viewer polling a file cannot flood the audit log.
type Auditor struct {
	Sink func(Event)
	Now  func() time.Time

	mu    sync.Mutex
	reads map[readKey]*Event
}

func (a *Auditor) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Record handles one operation.
func (a *Auditor) Record(ev Event) {
	if a == nil || a.Sink == nil {
		return
	}
	if !IsRead(ev.Method) {
		ev.Count = 1
		a.Sink(ev)
		return
	}
	k := readKey{ev.Session, ev.Method, ev.Path, a.now().Unix() / 60}
	a.mu.Lock()
	if a.reads == nil {
		a.reads = map[readKey]*Event{}
	}
	if cur, ok := a.reads[k]; ok {
		cur.Count++
		cur.Bytes += ev.Bytes
		cur.Status = ev.Status
	} else {
		ev.Count = 1
		a.reads[k] = &ev
	}
	a.mu.Unlock()
}

// Flush emits aggregated reads from minutes before the current one (all
// of them when force is set: shutdown, tests).
func (a *Auditor) Flush(force bool) {
	if a == nil || a.Sink == nil {
		return
	}
	cur := a.now().Unix() / 60
	a.mu.Lock()
	var due []readKey
	for k := range a.reads {
		if force || k.minute < cur {
			due = append(due, k)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].minute != due[j].minute {
			return due[i].minute < due[j].minute
		}
		if due[i].path != due[j].path {
			return due[i].path < due[j].path
		}
		return due[i].session+due[i].method < due[j].session+due[j].method
	})
	out := make([]Event, 0, len(due))
	for _, k := range due {
		out = append(out, *a.reads[k])
		delete(a.reads, k)
	}
	a.mu.Unlock()
	for _, ev := range out {
		a.Sink(ev)
	}
}

// Run flushes every 30 seconds and once more when ctx ends.
func (a *Auditor) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			a.Flush(true)
			return
		case <-t.C:
			a.Flush(false)
		}
	}
}
