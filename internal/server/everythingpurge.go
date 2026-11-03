package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"homestead/internal/audit"
	"homestead/internal/catalog"
	"homestead/internal/operation"
	"homestead/internal/purge"
	"homestead/internal/scheduler"
	"homestead/internal/store"
)

// EverythingPurgeJobID is the scheduler job that retries pending purges
// (S427, ADR-058 section 1); it is enabled only while one is pending.
const (
	EverythingPurgeJobID    = "people:everything-purge"
	EverythingPurgeInterval = 2 * time.Minute
)

// everythingBridgeImage is how every Everything bridge image starts.
const everythingBridgeImage = "homestead/everything-"

// dockerExec runs a command in a container. The arguments never carry a
// secret (a name and a subject only); a stopped container fails the call.
var dockerExec = func(ctx context.Context, container string, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", append([]string{"exec", container}, argv...)...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker exec in %s: %w: %s", container, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// everythingTargets lists the installed Everything apps: installed catalog
// packages with a bridge image service. The bridge's application name is the
// package name (the image build argument), the container is
// <package>-<service>.
func everythingTargets(st *store.Store, catalogDir string) func(context.Context) ([]purge.Target, error) {
	return func(context.Context) ([]purge.Target, error) {
		stacks, err := catalog.InstalledStacks(st)
		if err != nil {
			return nil, err
		}
		entries, err := catalog.Load(catalogDir)
		if err != nil {
			return nil, err
		}
		var out []purge.Target
		for _, name := range stacks {
			m, err := entries.Get(name)
			if err != nil {
				continue
			}
			for _, svc := range m.Services {
				if strings.HasPrefix(svc.Image, everythingBridgeImage) {
					out = append(out, purge.Target{App: m.Name, Container: m.Name + "-" + svc.Name})
					break
				}
			}
		}
		return out, nil
	}
}

// NewEverythingPurger builds the purge service the People hooks and the
// retry job share.
func NewEverythingPurger(st *store.Store, catalogDir string, now func() time.Time) *purge.Service {
	auditSvc := audit.New(st)
	return &purge.Service{
		DB: st.DB(), Apps: everythingTargets(st, catalogDir), Exec: dockerExec, Now: now,
		Audit: func(actor, action, outcome, detail string) {
			_, _ = auditSvc.Record(audit.Entry{Actor: actor, Action: action, Outcome: outcome, Detail: detail})
		},
	}
}

// everythingPurgeSource retries pending purges on a timer.
func everythingPurgeSource(p *purge.Service, now func() time.Time) scheduler.Source {
	return scheduler.SourceFunc(func() ([]scheduler.Job, error) {
		return []scheduler.Job{{
			ID:       EverythingPurgeJobID,
			Interval: EverythingPurgeInterval,
			Enabled:  p.HasPending(),
			Since:    now().Add(-EverythingPurgeInterval),
			Build: func() (operation.Definition, error) {
				return operation.Definition{Type: "people.purge-retry", Steps: []operation.StepDef{{
					Name: "retry", Run: func(ctx context.Context) error {
						p.Retry(ctx, scheduler.Actor, "")
						return nil
					},
				}}}, nil
			},
		}}, nil
	})
}
