package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"homestead/internal/operation"
)

// EventPollInterval paces state snapshots behind the stream. Operations
// run for minutes; sub-second latency buys nothing and burns disk reads.
const EventPollInterval = 500 * time.Millisecond

// EventHeartbeatInterval keeps idle streams alive through buffering
// proxies. Heartbeats are SSE comments, ignored by clients.
const EventHeartbeatInterval = 30 * time.Second

// MountOperations serves operation records (S062): fetch, list, plus a
// poll-backed SSE stream. The stream sends the current state immediately,
// then a state event per change, closing after the terminal state.
func (s *Server) MountOperations(svc *operation.Store) {
	s.mux.HandleFunc("GET /api/v1/operations", handleOperationList(svc))
	s.mux.HandleFunc("GET /api/v1/operations/{id}", handleOperationGet(svc))
	s.mux.HandleFunc("GET /api/v1/operations/{id}/events", handleOperationEvents(svc))
}

func operationLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return limit, nil
}

func handleOperationList(svc *operation.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, err := operationLimit(r)
		if err != nil {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		ops, err := svc.List(limit)
		if err != nil {
			http.Error(w, "failed to list operations", http.StatusInternalServerError)
			return
		}
		if ops == nil {
			ops = []operation.Operation{}
		}
		writeJSON(w, ops)
	}
}

func operationID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid operation id")
	}
	return id, nil
}

func handleOperationGet(svc *operation.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := operationID(r)
		if err != nil {
			http.Error(w, "invalid operation id", http.StatusBadRequest)
			return
		}
		op, err := svc.Get(id)
		if err != nil {
			http.Error(w, "operation not found", http.StatusNotFound)
			return
		}
		writeJSON(w, op)
	}
}

func handleOperationEvents(svc *operation.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := operationID(r)
		if err != nil {
			http.Error(w, "invalid operation id", http.StatusBadRequest)
			return
		}
		op, err := svc.Get(id)
		if err != nil {
			http.Error(w, "operation not found", http.StatusNotFound)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		poll := time.NewTicker(EventPollInterval)
		defer poll.Stop()
		heartbeat := time.NewTicker(EventHeartbeatInterval)
		defer heartbeat.Stop()
		previous, err := marshalState(op)
		if err != nil {
			http.Error(w, "failed to encode operation", http.StatusInternalServerError)
			return
		}
		if !sendState(w, flusher, previous) {
			return
		}
		if operation.Terminal(op.Status) {
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case <-poll.C:
				current, err := svc.Get(id)
				if err != nil {
					return
				}
				next, err := marshalState(current)
				if err != nil {
					return
				}
				if bytes.Equal(next, previous) {
					continue
				}
				previous = next
				if !sendState(w, flusher, next) {
					return
				}
				if operation.Terminal(current.Status) {
					return
				}
			}
		}
	}
}

func marshalState(op operation.Operation) ([]byte, error) {
	return json.Marshal(op)
}

func sendState(w http.ResponseWriter, flusher http.Flusher, state []byte) bool {
	if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", state); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
