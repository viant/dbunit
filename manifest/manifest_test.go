package manifest

import (
	"context"
	"encoding/json"
	"github.com/viant/dbunit/fixture"
	"testing"
)

func TestVersionedManifest(t *testing.T) {
	c, err := Load(context.Background(), fixture.Inline(), fixture.Bytes([]byte(`version: 1
name: embedded example
state:
  id: 9007199254740993
prepare:
  - table: [users]
    operation: insert
    source:
      path: prepare/users.json
expect:
  - table: [users]
    scope: full
    expected:
      path: expect/users.json
`), "yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.State["id"] != json.Number("9007199254740993") || c.Prepare[0].Source.Path != "prepare/users.json" {
		t.Fatalf("bad manifest: %#v", c)
	}
}
func TestRejectInvalidManifest(t *testing.T) {
	for _, tc := range []struct{ data, format string }{
		{`{"version":2}`, "json"}, {`{"version":1,"preapre":[]}`, "json"},
		{"version: 1\nversion: 2", "yaml"}, {"version: 1\nstate: &x {x: 1}\nprepare: *x", "yaml"},
		{"version: 1\n---\nversion: 1", "yaml"},
	} {
		if _, err := Load(context.Background(), fixture.Inline(), fixture.Bytes([]byte(tc.data), tc.format)); err == nil {
			t.Fatalf("invalid accepted: %s", tc.data)
		}
	}
}
