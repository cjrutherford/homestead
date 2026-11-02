package craft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"homestead/internal/operation"
	"homestead/internal/release"
)

// Everything bridge builds (ADR-057). An Everything app is built on the
// appliance from a pinned source archive that ships with the appliance and
// the bridge build context that ships beside it. They reuse the craft
// build machinery (staging, disk preflight, private context copy, pinned
// Dockerfile, build, record, the local-image gate) but not the signed craft
// manifest: the integrity of the source is the SHA-256 in the app
// definition (cross-checked with third_party/everything/pin.yaml by a
// repository test) and the signed Homestead release that ships both.

// BridgeOperationType is the operation type of an Everything build.
const BridgeOperationType = "everything.build"

var bridgeAppPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// BridgeApp is one Everything app definition, the part of
// scripts/everything/apps/<app>.json the appliance needs to build it.
type BridgeApp struct {
	App     string `json:"app"`
	Version string `json:"version"`
	Archive string `json:"archive"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// BridgeDefinitionPath is where the definition of app lives inside the
// bridge build context.
func BridgeDefinitionPath(contextDir, app string) string {
	return filepath.Join(contextDir, "scripts", "everything", "apps", app+".json")
}

// LoadBridgeApp reads and validates the definition of app from the bridge
// build context.
func (p *Pipeline) LoadBridgeApp(app string) (BridgeApp, error) {
	if !bridgeAppPattern.MatchString(app) {
		return BridgeApp{}, failed(CheckInput, fmt.Errorf("%w: invalid app name", ErrInput))
	}
	if p.BridgeContextDir == "" || p.BridgeSourcesDir == "" {
		return BridgeApp{}, failed(CheckInput, fmt.Errorf("%w: Everything builds are not available on this appliance", ErrInput))
	}
	raw, err := os.ReadFile(BridgeDefinitionPath(p.BridgeContextDir, app))
	if err != nil {
		return BridgeApp{}, failed(CheckInput, fmt.Errorf("%w: %s is not an Everything app", ErrInput, app))
	}
	var a BridgeApp
	if err := json.Unmarshal(raw, &a); err != nil {
		return BridgeApp{}, failed(CheckManifest, fmt.Errorf("app definition: %v", err))
	}
	if a.App != app || !versionPattern.MatchString(a.Version) || !sha256Pattern.MatchString(a.SHA256) ||
		!fileNamePattern.MatchString(a.Archive) || a.Size < 1 || a.Size > release.MaxArtifactBytes {
		return BridgeApp{}, failed(CheckManifest, errors.New("app definition is incomplete or malformed"))
	}
	return a, nil
}

// verified turns a definition into the Verified shape the craft steps use.
// The "manifest" context digest is the digest of the shipped bridge
// context; stepContext compares the private build copy with it.
func (p *Pipeline) bridgeVerified(a BridgeApp) (Verified, error) {
	digest, err := TreeDigest(p.BridgeContextDir)
	if err != nil {
		return Verified{}, failed(CheckContext, err)
	}
	return Verified{
		App: App{Name: a.App, Version: a.Version,
			Artifact: Artifact{Name: a.Archive, SHA256: a.SHA256, Size: a.Size}},
		Manifest: Manifest{ContextDigest: digest},
	}, nil
}

// BridgePlan reports what an Everything build of app would build, after
// checking the definition, the shipped archive and the disk.
func (p *Pipeline) BridgePlan(ctx context.Context, app string) (Plan, error) {
	a, err := p.LoadBridgeApp(app)
	if err != nil {
		return Plan{}, err
	}
	v, err := p.bridgeVerified(a)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{App: a.App, Version: a.Version, Tag: BridgeTag(a.App, a.Version), ArtifactName: a.Archive,
		ArtifactSHA256: a.SHA256, ArtifactSize: a.Size, Source: "bundled", ContextDigest: v.Manifest.ContextDigest,
		DiskRequired: p.required(a.Size)}
	path := filepath.Join(p.BridgeSourcesDir, a.Archive)
	if err := checkLocalArtifact(path, v.App.Artifact); err != nil {
		return plan, err
	}
	if err := scanArchive(path, a.Archive, p.maxExpanded()); err != nil {
		return plan, failed(CheckArtifact, err)
	}
	return plan, p.preflight(ctx, a.Size)
}

// BridgeDefinition returns the Everything build operation: verify the pin,
// stage the shipped archive and a private copy of the context, build with
// the build network, record. A failure leaves no tag and no record.
func (p *Pipeline) BridgeDefinition(app, actor string) (operation.Definition, error) {
	a, err := p.LoadBridgeApp(app)
	if err != nil {
		return operation.Definition{}, err
	}
	req := Request{App: app, Version: a.Version, Actor: actor,
		ArtifactPath: filepath.Join(p.BridgeSourcesDir, a.Archive)}
	r := p.newRun(req)
	r.bridge = &a
	step := func(name string, fn func(ctx context.Context) error) operation.StepDef {
		return operation.StepDef{Name: name, Run: func(ctx context.Context) error {
			err := fn(ctx)
			if err != nil {
				r.recordFailure(ctx, err)
			}
			return err
		}}
	}
	return operation.Definition{
		Type: BridgeOperationType,
		Steps: []operation.StepDef{
			step("verify-pin", func(ctx context.Context) error {
				if !r.p.acquire(ctx) {
					return failed(CheckCancelled, ctx.Err())
				}
				r.locked = true
				v, err := r.p.bridgeVerified(a)
				r.v = v
				return err
			}),
			step("preflight-disk", r.stepPreflight),
			step("stage-source", r.stepArtifact),
			step("scan-archive", r.stepScan),
			step("verify-context", r.stepContext),
			step("build", r.stepBuild),
			step("record", r.stepRecord),
		},
		Cleanup: r.cleanup,
	}, nil
}

// IsBridgeApp reports whether app has an Everything definition on this
// appliance.
func (p *Pipeline) IsBridgeApp(app string) bool {
	_, err := p.LoadBridgeApp(app)
	return err == nil
}

// BridgeCheck plans an Everything build and records the outcome as the
// app's verdict, like Check does for a craft app.
func (p *Pipeline) BridgeCheck(ctx context.Context, app, actor string) (Plan, Verdict, error) {
	plan, err := p.BridgePlan(ctx, app)
	verdict := Verdict{App: app, Version: plan.Version, Actor: actor}
	if err != nil {
		var ce *CheckError
		verdict.State, verdict.Refusal, verdict.Detail = StateFailed, CheckBuild, err.Error()
		if errors.As(err, &ce) {
			verdict.Refusal = ce.Check
			if ce.Check == CheckInput {
				return plan, verdict, err
			}
		}
	} else {
		verdict.State = StateVerified
		verdict.Detail = fmt.Sprintf("checked %s: the shipped source archive matches its pin (%s)", plan.Version, plan.Source)
	}
	if serr := p.Store.SaveVerdict(verdict); serr != nil && err == nil {
		return plan, verdict, serr
	}
	return plan, verdict, err
}
