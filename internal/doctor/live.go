package doctor

import (
	"context"
	"fmt"
	"time"
)

// Probe is one live check (S316). It must be read-only and honour ctx;
// Name, Group and Timeout describe it, so a probe that does not answer in
// time is reported as unknown under its own name.
type Probe struct {
	Name    string
	Group   string
	Timeout time.Duration
	Run     func(ctx context.Context) []Check
}

// Live bounds: every probe has its own timeout (Probe.Timeout, else
// DefaultProbeTimeout) and the whole run has a budget.
const (
	DefaultProbeTimeout = 5 * time.Second
	DefaultBudget       = 20 * time.Second
)

// RunLive runs probes concurrently within budget and returns their checks
// in probe order. A probe that has not answered by its timeout, or by the
// budget, yields one unknown check. RunLive returns by the budget even if
// a probe ignores its context.
func RunLive(ctx context.Context, budget time.Duration, probes []Probe) []Check {
	if budget <= 0 {
		budget = DefaultBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	results := make([][]Check, len(probes))
	done := make([]chan struct{}, len(probes))
	for i, p := range probes {
		done[i] = make(chan struct{})
		timeout := p.Timeout
		if timeout <= 0 {
			timeout = DefaultProbeTimeout
		}
		go func() {
			defer close(done[i])
			pctx, pcancel := context.WithTimeout(ctx, timeout)
			defer pcancel()
			finished := make(chan []Check, 1)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						finished <- nil
					}
				}()
				finished <- p.Run(pctx)
			}()
			select {
			case out := <-finished:
				results[i] = out
			case <-pctx.Done():
				results[i] = []Check{{Name: p.Name, Group: p.Group, Status: StatusUnknown,
					Detail: fmt.Sprintf("did not answer within %s", timeout.Round(100*time.Millisecond))}}
			}
		}()
	}
	var out []Check
	for i := range probes {
		<-done[i]
		for _, c := range results[i] {
			c.Live = true
			out = append(out, c)
		}
	}
	return out
}
