package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const selfManagedManifest = `name: mealie
version: 1.0.0
title: Mealie
blurb: Test install.
description: Test self-managed installation.
category: media
icon: mdi:food
services:
- name: web
  image: example/mealie:1.0.0
  exposes: web
  containerport: 9000
serve: host
requirements:
  memorymb: 64
  storage: []
  ports: []
exposure:
  package: mealie
  endpoints:
  - endpoint: web
    recommended: local
    allowwider: true
backuppaths: [data]
`

// S398: an app that keeps its own accounts installs without per-person
// choices, and any per-person claim for it is refused before an operation.
func TestSelfManagedInstallHandlerMatrix(t *testing.T) {
	a := installHandlerRig(t)
	post := func(path string, body map[string]any) (int, string) {
		t.Helper()
		raw, _ := json.Marshal(body)
		rec := a.req(http.MethodPost, path, string(raw), a.owner.token)
		return rec.Code, rec.Body.String()
	}
	base := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 9000}}
	code, plan := post("/api/v1/catalog/mealie/plan", base)
	if code != http.StatusOK {
		t.Fatalf("plan: %d %s", code, plan)
	}
	var reviewed struct {
		Digest string `json:"digest"`
		Access struct {
			Mode   string `json:"mode"`
			Notice string `json:"notice"`
		} `json:"access"`
	}
	if err := json.Unmarshal([]byte(plan), &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.Access.Mode != "self-managed" || !strings.Contains(reviewed.Access.Notice, "Mealie keeps its own accounts") {
		t.Fatalf("access review: %s", plan)
	}
	// Any per-person choice is refused before an operation exists.
	withChoice := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 9000},
		"choices": []map[string]any{{"person_id": 1, "role": "user"}}}
	if code, body := post("/api/v1/catalog/mealie/access-plan", withChoice); code != http.StatusUnprocessableEntity {
		t.Fatalf("access-plan with choices: %d %s", code, body)
	}
	code, accessBody := post("/api/v1/catalog/mealie/access-plan", base)
	if code != http.StatusOK {
		t.Fatalf("access-plan: %d %s", code, accessBody)
	}
	var ap struct {
		Digest string `json:"digest"`
	}
	_ = json.Unmarshal([]byte(accessBody), &ap)
	for name, setup := range map[string]map[string]any{
		"choices in approval": {"access": map[string]any{"digest": ap.Digest, "choices": []map[string]any{{"person_id": 1, "role": "user"}}}},
		"admin choice":        {"access": map[string]any{"digest": ap.Digest, "choices": []map[string]any{{"person_id": 1, "role": "admin"}}}},
		"legacy people":       {"people": []int{1}},
	} {
		body := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 9000}, "digest": reviewed.Digest, "setup": setup}
		if code, got := post("/api/v1/catalog/mealie/install", body); code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d %s", name, code, got)
		}
	}
	topLevel := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 9000}, "digest": reviewed.Digest,
		"choices": []map[string]any{{"person_id": 1, "role": "user"}}}
	if code, got := post("/api/v1/catalog/mealie/install", topLevel); code != http.StatusUnprocessableEntity {
		t.Fatalf("top-level choices: %d %s", code, got)
	}
	stale := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 9000}, "digest": reviewed.Digest,
		"setup": map[string]any{"access": map[string]any{"digest": "stale", "choices": []any{}}}}
	if code, got := post("/api/v1/catalog/mealie/install", stale); code != http.StatusConflict {
		t.Fatalf("stale access digest: %d %s", code, got)
	}
	if n := installOperationCount(t, a); n != 0 {
		t.Fatalf("operation created for refused install: %d", n)
	}
	// With no claim, the install proceeds to the operation boundary, with or
	// without the reviewed access digest.
	for name, setup := range map[string]map[string]any{
		"no setup":        nil,
		"reviewed digest": {"access": map[string]any{"digest": ap.Digest, "choices": []any{}}},
	} {
		body := map[string]any{"domain": "lan", "container_ports": map[string]int{"web": 9000}, "digest": reviewed.Digest}
		if setup != nil {
			body["setup"] = setup
		}
		if code, got := post("/api/v1/catalog/mealie/install", body); code != http.StatusInternalServerError || !strings.Contains(got, "test install stopped") {
			t.Fatalf("%s did not reach the operation boundary: %d %s", name, code, got)
		}
	}
	// Managed apps are unchanged.
	code, immich := post("/api/v1/catalog/immich/plan", installMatrixBody())
	if code != http.StatusOK || !strings.Contains(immich, `"mode":"managed"`) {
		t.Fatalf("immich: %d %s", code, immich)
	}
}
