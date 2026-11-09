package apiclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPeopleClientErrorCodeAndPaths(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		if r.URL.Path == "/api/v1/people/9/remove" {
			w.Header().Set("X-Homestead-Error", PeopleNoToken)
			http.Error(w, "no token", http.StatusUnprocessableEntity)
			return
		}
		_, _ = w.Write([]byte(`{"person":{"id":9,"username":"sam"}}`))
	}))
	defer ts.Close()
	c := New(ts.URL)
	ctx := context.Background()
	p, err := c.GrantPersonApp(ctx, 9, "immich", true)
	if err != nil || p.Username != "sam" {
		t.Fatalf("%v %+v", err, p)
	}
	err = c.RemovePerson(ctx, 9)
	se, ok := err.(*StatusError)
	if !ok || se.Code != PeopleNoToken {
		t.Fatalf("err %v", err)
	}
	if got[0] != `PUT /api/v1/people/9/apps/immich/admin {"confirm":true}` {
		t.Fatalf("got %q", got[0])
	}
}
