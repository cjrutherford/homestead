package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homestead/internal/operation"
	"homestead/internal/store"
)

func operationServer(t *testing.T) (*Server, *operation.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := operation.New(st)
	srv := New("127.0.0.1:0")
	srv.MountOperations(svc)
	return srv, svc
}

func TestOperationGet(t *testing.T) {
	srv, svc := operationServer(t)
	created, err := svc.Create("test.op", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != 1 {
		t.Fatalf("id = %d, want 1", created.ID)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operations/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var body struct {
		ID     int64  `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Steps  []any  `json:"steps"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != 1 || body.Type != "test.op" || body.Status != "pending" || body.Steps == nil {
		t.Fatalf("body = %+v", body)
	}
}

func TestOperationGetFailures(t *testing.T) {
	srv, _ := operationServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operations/999", nil)
	req.SetPathValue("id", "999")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/operations/nope/events", nil)
	req.SetPathValue("id", "nope")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET status = %d, want 400", rec.Code)
	}
}

type sseEvent struct {
	name string
	data string
}

func readEvents(t *testing.T, url string, max int) []sseEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if content := res.Header.Get("Content-Type"); content != "text/event-stream" {
		t.Fatalf("Content-Type = %q", content)
	}
	var events []sseEvent
	var name, data strings.Builder
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				events = append(events, sseEvent{name: name.String(), data: data.String()})
				name.Reset()
				data.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		if key == "event" {
			name.WriteString(strings.TrimSpace(value))
		} else if key == "data" {
			data.WriteString(strings.TrimSpace(value))
		}
		if len(events) >= max {
			break
		}
	}
	return events
}

func TestOperationEventsTerminalCloses(t *testing.T) {
	srv, svc := operationServer(t)
	created, err := svc.Create("test.op", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.AddStep(created.ID, "only"); err != nil {
		t.Fatalf("AddStep: %v", err)
	}
	if err := svc.Transition(created.ID, operation.StatusRunning, "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.Transition(created.ID, operation.StatusSucceeded, `{"ok":true}`, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	events := readEvents(t, ts.URL+"/api/v1/operations/1/events", 10)
	if len(events) != 1 || events[0].name != "state" {
		t.Fatalf("events = %+v, want single state", events)
	}
	var body struct {
		Status string `json:"status"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(events[0].data), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "succeeded" || body.Result != `{"ok":true}` {
		t.Fatalf("body = %+v", body)
	}
}

func TestOperationEventsStreamTransitions(t *testing.T) {
	srv, svc := operationServer(t)
	created, err := svc.Create("test.op", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	done := make(chan []sseEvent, 1)
	go func() {
		done <- readEvents(t, ts.URL+"/api/v1/operations/1/events", 10)
	}()
	// Let the stream deliver its initial state before transitioning.
	time.Sleep(700 * time.Millisecond)
	if err := svc.Transition(created.ID, operation.StatusRunning, "", ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if err := svc.Transition(created.ID, operation.StatusSucceeded, "", ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	events := <-done
	statuses := map[string]bool{}
	for _, event := range events {
		var body struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(event.data), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		statuses[body.Status] = true
	}
	for _, want := range []string{"pending", "running", "succeeded"} {
		if !statuses[want] {
			t.Fatalf("statuses = %v, want %q", statuses, want)
		}
	}
}

func TestOperationList(t *testing.T) {
	srv, svc := operationServer(t)
	for _, typ := range []string{"a.one", "b.two"} {
		if _, err := svc.Create(typ, ""); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/operations?limit=1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	var body []struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 || body[0].Type != "b.two" {
		t.Fatalf("body = %+v, want newest first", body)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/operations?limit=bogus", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET status = %d, want 400", rec.Code)
	}
}
