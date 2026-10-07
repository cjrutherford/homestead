package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"homestead/internal/operation"
)

// GetOperation fetches one operation record (S062).
func (c *Client) GetOperation(ctx context.Context, id int64) (operation.Operation, error) {
	var op operation.Operation
	if err := c.get(ctx, fmt.Sprintf("/api/v1/operations/%d", id), &op); err != nil {
		return operation.Operation{}, err
	}
	return op, nil
}

// ListOperations returns the newest operation records first (S064).
func (c *Client) ListOperations(ctx context.Context, limit int) ([]operation.Operation, error) {
	path := "/api/v1/operations"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, c.statusError(req, res)
	}
	var ops []operation.Operation
	if err := json.NewDecoder(res.Body).Decode(&ops); err != nil {
		return nil, fmt.Errorf("api: decode response: %w", err)
	}
	if ops == nil {
		ops = []operation.Operation{}
	}
	return ops, nil
}

// StreamOperation follows the SSE state stream, calling onState for every
// state event including the first. It returns when the server closes the
// stream (terminal states) or the context ends. Transport failures error;
// the caller decides terminality from op.Status.
func (c *Client) StreamOperation(ctx context.Context, id int64, onState func(operation.Operation)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/operations/%d/events", c.base, id), nil)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return &StatusError{
			Method: http.MethodGet,
			Path:   req.URL.Path,
			Status: res.StatusCode,
			Body:   strings.TrimSpace(string(excerpt)),
		}
	}
	return readStates(res.Body, onState)
}

// readStates decodes SSE state events until the stream closes.
func readStates(body io.Reader, onState func(operation.Operation)) error {
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		var op operation.Operation
		if err := json.Unmarshal([]byte(data.String()), &op); err != nil {
			return fmt.Errorf("api: decode event: %w", err)
		}
		data.Reset()
		onState(op)
		return nil
	}
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data.WriteString(strings.TrimSpace(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("api: read stream: %w", err)
	}
	return flush()
}
