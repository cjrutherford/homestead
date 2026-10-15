package craft

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"homestead/internal/release"
	"homestead/internal/store"
)

// Verdict states.
const (
	StateVerified = "verified"
	StateBuilt    = "built"
	StateFailed   = "failed"
)

// Verdict is the latest verification or build outcome for one app,
// shown by the API, CLI and dashboard alike.
type Verdict struct {
	App     string `json:"app"`
	Version string `json:"version"`
	Counter int64  `json:"counter"`
	State   string `json:"state"`
	// Refusal names the failing check (see the Check* constants); empty
	// when nothing was refused.
	Refusal    string    `json:"refusal"`
	Detail     string    `json:"detail"`
	KeyID      string    `json:"key_id"`
	Override   bool      `json:"override"`
	Actor      string    `json:"actor"`
	VerifiedAt time.Time `json:"verified_at"`
}

// Image is the record of one locally built image.
type Image struct {
	App            string    `json:"app"`
	Version        string    `json:"version"`
	Tag            string    `json:"tag"`
	ImageID        string    `json:"image_id"`
	Counter        int64     `json:"counter"`
	KeyID          string    `json:"key_id"`
	ArtifactSHA256 string    `json:"artifact_sha256"`
	ContextDigest  string    `json:"context_digest"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	BuiltAt        time.Time `json:"built_at"`
}

// Store keeps baselines, verdicts and image records in SQLite (ADR-003).
type Store struct{ db *sql.DB }

// NewStore returns a Store over an open, migrated store.
func NewStore(s *store.Store) *Store { return &Store{db: s.DB()} }

// Baseline returns the recorded per-app version and counter (zero when
// nothing is recorded).
func (s *Store) Baseline(app string) (release.Installed, error) {
	var in release.Installed
	err := s.db.QueryRow(`SELECT version, counter FROM craft_baseline WHERE app = ?`, app).Scan(&in.Version, &in.Counter)
	if errors.Is(err, sql.ErrNoRows) {
		return release.Installed{}, nil
	}
	if err != nil {
		return release.Installed{}, fmt.Errorf("craft: baseline: %w", err)
	}
	return in, nil
}

// HighWater is the highest manifest counter any craft app was built
// from. Manifests of one signing train share one counter sequence, so a
// manifest below it is a replay even for an app never built before.
func (s *Store) HighWater() (int64, error) {
	var n sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(counter) FROM craft_baseline`).Scan(&n); err != nil {
		return 0, fmt.Errorf("craft: high-water: %w", err)
	}
	return n.Int64, nil
}

// BaselineDigests returns the artifact and build-context digests of the
// build that set the app's baseline (empty when unknown).
func (s *Store) BaselineDigests(app string) (artifact, context string, err error) {
	err = s.db.QueryRow(`SELECT artifact_sha256, context_digest FROM craft_baseline WHERE app = ?`, app).Scan(&artifact, &context)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("craft: baseline digests: %w", err)
	}
	return artifact, context, nil
}

// SaveVerdict upserts the latest verdict for an app.
func (s *Store) SaveVerdict(v Verdict) error {
	at := v.VerifiedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO craft_verdicts (app, version, counter, state, refusal, detail, key_id, override, actor, verified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(app) DO UPDATE SET version = excluded.version, counter = excluded.counter, state = excluded.state,
			refusal = excluded.refusal, detail = excluded.detail, key_id = excluded.key_id, override = excluded.override,
			actor = excluded.actor, verified_at = excluded.verified_at`,
		v.App, v.Version, v.Counter, v.State, v.Refusal, v.Detail, v.KeyID, v.Override, v.Actor, at.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("craft: save verdict: %w", err)
	}
	return nil
}

// Verdicts lists the latest verdict per app.
func (s *Store) Verdicts() ([]Verdict, error) {
	rows, err := s.db.Query(`SELECT app, version, counter, state, refusal, detail, key_id, override, actor, verified_at FROM craft_verdicts ORDER BY app`)
	if err != nil {
		return nil, fmt.Errorf("craft: verdicts: %w", err)
	}
	defer rows.Close()
	out := []Verdict{}
	for rows.Next() {
		var v Verdict
		var at string
		if err := rows.Scan(&v.App, &v.Version, &v.Counter, &v.State, &v.Refusal, &v.Detail, &v.KeyID, &v.Override, &v.Actor, &at); err != nil {
			return nil, fmt.Errorf("craft: verdicts: %w", err)
		}
		v.VerifiedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ErrNoImage marks an app version with no recorded local build.
var ErrNoImage = errors.New("craft: no recorded local image")

// Image returns the recorded build of one app version.
func (s *Store) Image(app, version string) (Image, error) {
	row := s.db.QueryRow(imageSelect+` WHERE app = ? AND version = ?`, app, version)
	img, err := scanImage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Image{}, ErrNoImage
	}
	return img, err
}

// Images lists every recorded build, sorted by app then version.
func (s *Store) Images() ([]Image, error) {
	rows, err := s.db.Query(imageSelect + ` ORDER BY app, version`)
	if err != nil {
		return nil, fmt.Errorf("craft: images: %w", err)
	}
	defer rows.Close()
	out := []Image{}
	for rows.Next() {
		img, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

const imageSelect = `SELECT app, version, tag, image_id, counter, key_id, artifact_sha256, context_digest, manifest_sha256, built_at FROM craft_images`

type scanner interface{ Scan(dest ...any) error }

func scanImage(r scanner) (Image, error) {
	var img Image
	var at string
	if err := r.Scan(&img.App, &img.Version, &img.Tag, &img.ImageID, &img.Counter, &img.KeyID, &img.ArtifactSHA256, &img.ContextDigest, &img.ManifestSHA256, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Image{}, err
		}
		return Image{}, fmt.Errorf("craft: image: %w", err)
	}
	img.BuiltAt, _ = time.Parse(time.RFC3339Nano, at)
	return img, nil
}

// putImage records (or replaces) a build and moves the app's baseline,
// atomically.
// ErrBaselineLowered marks a record refused because a newer manifest
// finished first and the baseline may not move backwards without the
// audited override.
var ErrBaselineLowered = errors.New("craft: a newer manifest was recorded meanwhile; the baseline would move backwards")

func (s *Store) putImage(img Image, override bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("craft: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	at := img.BuiltAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if _, err := tx.Exec(`INSERT INTO craft_images (app, version, tag, image_id, counter, key_id, artifact_sha256, context_digest, manifest_sha256, built_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(app, version) DO UPDATE SET tag = excluded.tag, image_id = excluded.image_id, counter = excluded.counter,
			key_id = excluded.key_id, artifact_sha256 = excluded.artifact_sha256, context_digest = excluded.context_digest,
			manifest_sha256 = excluded.manifest_sha256, built_at = excluded.built_at`,
		img.App, img.Version, img.Tag, img.ImageID, img.Counter, img.KeyID, img.ArtifactSHA256, img.ContextDigest, img.ManifestSHA256, at.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("craft: record image: %w", err)
	}
	// Guarded upsert: the train-wide high-water mark and this app's
	// baseline may only move forward, unless the owner overrode.
	var high sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(counter) FROM craft_baseline`).Scan(&high); err != nil {
		return fmt.Errorf("craft: high-water: %w", err)
	}
	if !override && img.Counter < high.Int64 {
		return ErrBaselineLowered
	}
	res, err := tx.Exec(`INSERT INTO craft_baseline (app, version, counter, recorded_at, artifact_sha256, context_digest) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(app) DO UPDATE SET version = excluded.version, counter = excluded.counter, recorded_at = excluded.recorded_at,
			artifact_sha256 = excluded.artifact_sha256, context_digest = excluded.context_digest
		WHERE excluded.counter >= craft_baseline.counter OR ?`,
		img.App, img.Version, img.Counter, time.Now().UTC().Format(time.RFC3339Nano), img.ArtifactSHA256, img.ContextDigest, override)
	if err != nil {
		return fmt.Errorf("craft: set baseline: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBaselineLowered
	}
	return tx.Commit()
}

func (s *Store) deleteImage(app, version string) error {
	_, err := s.db.Exec(`DELETE FROM craft_images WHERE app = ? AND version = ?`, app, version)
	if err != nil {
		return fmt.Errorf("craft: delete image record: %w", err)
	}
	return nil
}
