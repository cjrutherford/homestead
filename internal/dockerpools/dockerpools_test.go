package dockerpools

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

func TestMergeKeepsOtherKeys(t *testing.T) {
	in := []byte(`{"log-driver":"json-file","log-opts":{"max-size":"10m"}}`)
	out, changed, err := Merge(in, "10.200.0.0/16")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"log-driver", "log-opts", Key} {
		if _, ok := doc[k]; !ok {
			t.Errorf("key %s lost", k)
		}
	}
	n, present, _ := Capacity(out)
	if !present || n != 256 {
		t.Errorf("capacity = %d present=%v, want 256", n, present)
	}
}

func TestMergeEmptyAndIdempotent(t *testing.T) {
	out, changed, err := Merge(nil, "10.200.0.0/16")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	again, changed, err := Merge(out, "10.201.0.0/16")
	if err != nil || changed || string(again) != string(out) {
		t.Fatalf("second merge changed=%v err=%v", changed, err)
	}
}

func TestMergeLeavesOwnerPoolsAndBrokenJSON(t *testing.T) {
	own := []byte(`{"default-address-pools":[{"base":"172.30.0.0/16","size":24}]}`)
	out, changed, _ := Merge(own, "10.200.0.0/16")
	if changed || string(out) != string(own) {
		t.Error("owner pools were changed")
	}
	if _, _, err := Merge([]byte(`{nope`), "10.200.0.0/16"); err == nil {
		t.Error("broken JSON must be refused, not overwritten")
	}
}

func TestCapacity(t *testing.T) {
	if n, present, _ := Capacity([]byte(`{}`)); present || n != 0 {
		t.Error("absent key")
	}
	n, _, _ := Capacity([]byte(`{"default-address-pools":[{"base":"10.0.0.0/16","size":24},{"base":"172.20.0.0/24","size":24}]}`))
	if n != 257 {
		t.Errorf("capacity = %d, want 257", n)
	}
	if _, _, err := Capacity([]byte(`{`)); err == nil {
		t.Error("want error")
	}
}

func TestChooseSkipsHostNetworks(t *testing.T) {
	got, err := Choose([]netip.Prefix{netip.MustParsePrefix("10.200.5.0/24")})
	if err != nil || got != "10.201.0.0/16" {
		t.Fatalf("got %q err=%v", got, err)
	}
	var all []netip.Prefix
	for _, c := range Candidates {
		all = append(all, netip.MustParsePrefix(c))
	}
	if _, err := Choose(all); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Error("want refusal")
	}
}
