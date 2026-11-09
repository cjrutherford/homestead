package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func myAppsStub(t *testing.T) *httptest.Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/me/apps":
			_, _ = w.Write([]byte(`{"apps":[{"name":"immich","title":"Immich","icon":"photo","role":"owner","url":"https://homestead.lan/apps/immich/","local_url":"https://homestead.lan/apps/immich/","public_url":"https://home.example.com/apps/immich/"}]}`))
		case "/api/v1/people/4/apps":
			_, _ = w.Write([]byte(`{"apps":[{"name":"immich","title":"Immich","icon":"photo","role":"user","url":"https://homestead.lan/apps/immich/","local_url":"https://homestead.lan/apps/immich/"}]}`))
		case "/api/v1/people":
			_, _ = w.Write([]byte(`{"token_kept":true,"owner":"chris","people":[{"id":4,"display_name":"Sam","username":"sam","state":"active","grants":[]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestAppsMineListsOwnerApps(t *testing.T) {
	ts := myAppsStub(t)
	code, out, _ := invoke(t, []string{"--server", ts.URL, "apps", "mine"})
	if code != exitOK || !strings.Contains(out, "immich  Immich  owner  local https://homestead.lan/apps/immich/  public https://home.example.com/apps/immich/") {
		t.Fatalf("exit %d out %q", code, out)
	}
	code, out, _ = invoke(t, []string{"--server", ts.URL, "apps", "mine", "--json"})
	if code != exitOK || !strings.Contains(out, `"role": "owner"`) {
		t.Fatalf("json: exit %d out %q", code, out)
	}
}

func TestPeopleAppsByUsernameAndID(t *testing.T) {
	ts := myAppsStub(t)
	for _, who := range []string{"sam", "4"} {
		code, out, _ := invoke(t, []string{"--server", ts.URL, "people", "apps", who})
		if code != exitOK || !strings.Contains(out, "immich  Immich  user  ") {
			t.Fatalf("%s: exit %d out %q", who, code, out)
		}
	}
	if code, _, _ := invoke(t, []string{"--server", ts.URL, "people", "apps"}); code != exitUsage {
		t.Fatalf("no person: exit %d", code)
	}
}
