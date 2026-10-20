package fixture

import (
	"context"
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"
)

//go:embed testdata
var assets embed.FS

func TestEmbedIsolationAndExplicitRoot(t *testing.T) {
	ctx := context.Background()
	one, two := Embed(&assets, "testdata/one"), Embed(&assets, "testdata/two")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, tc := range []struct {
				r    Resolver
				want string
			}{{one, "one"}, {two, "two"}} {
				rows, err := tc.r.Load(ctx, File("rows.json"))
				if err != nil {
					t.Error(err)
					return
				}
				if rows[0]["label"] != tc.want {
					t.Errorf("root leakage: %v", rows)
				}
			}
		}()
	}
	wg.Wait()
	if _, err := one.Read(ctx, File("missing.sql")); err == nil {
		t.Fatal("missing embed asset succeeded")
	}
	if _, err := one.Read(ctx, File("../two/rows.json")); err == nil {
		t.Fatal("traversal succeeded")
	}
}
func TestFSDirectoryAndNoImplicitCWD(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "users.json"), []byte(`[{"id":9007199254740993}]`), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := Directory(root).Load(ctx, File("users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0]["id"] != json.Number("9007199254740993") {
		t.Fatalf("lost precision: %#v", rows)
	}
	for _, r := range []Resolver{Directory("relative"), Inline()} {
		if _, err := r.Read(ctx, File("users.json")); err == nil {
			t.Fatal("implicit root accepted")
		}
	}
	fsys := fstest.MapFS{"root/schema.sql": {Data: []byte("CREATE TABLE x(id INTEGER);")}}
	content, err := FS(fsys, "root").Read(ctx, File("schema.sql"))
	if err != nil || len(content) == 0 {
		t.Fatalf("FS schema: %q %v", content, err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.json"), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Directory(root).Load(ctx, File("link/secret.json")); err == nil {
		t.Fatal("symlink root escape accepted")
	}
}
func TestCSVAndNDJSON(t *testing.T) {
	ctx := context.Background()
	src := Bytes([]byte("id,name,note\n001,\"hello\nworld\",\\N\n002,,text\n"), "csv")
	rows, err := Inline().Load(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["id"] != "001" || rows[0]["name"] != "hello\nworld" || rows[0]["note"] != nil || rows[1]["name"] != "" {
		t.Fatalf("CSV semantics: %#v", rows)
	}
	for _, bad := range []Source{Bytes([]byte("a,a\n1,2"), "csv"), Bytes([]byte("a,b\n1"), "csv"), Bytes([]byte(`[] {}`), "json"), Bytes([]byte(`null`), "json"), Bytes([]byte(`[{"a":1},null]`), "json"), Bytes([]byte("{\"id\":1}\n{broken}"), "ndjson")} {
		if _, err := Inline().Load(ctx, bad); err == nil {
			t.Fatalf("invalid data accepted: %s", bad.Content)
		}
	}
	rows, err = Inline().Load(ctx, Bytes([]byte("{\"id\":9007199254740993}\n{\"id\":2}\n"), "jsonl"))
	if err != nil || len(rows) != 2 {
		t.Fatalf("NDJSON: %v %v", rows, err)
	}
}
func TestMapsCopyAndTypedPresence(t *testing.T) {
	ctx := context.Background()
	input := []map[string]any{{"nested": map[string]any{"v": 1}, "items": []any{map[string]any{"v": 2}}, "empty": []any{}, "nil": nil}}
	rows, err := Inline().Load(ctx, Maps(input))
	if err != nil {
		t.Fatal(err)
	}
	rows[0]["nested"].(map[string]any)["v"] = 7
	if input[0]["nested"].(map[string]any)["v"] != 1 {
		t.Fatal("caller map changed")
	}
	if rows[0]["empty"] == nil {
		t.Fatal("empty array lost")
	}
	type flags struct{ ID, Name bool }
	type row struct {
		ID   *int64  `json:"id"`
		Name *string `json:"name"`
		Has  *flags  `setMarker:"true" json:"-"`
	}
	zero := int64(0)
	rows, err = Inline().Load(ctx, Typed([]*row{{ID: &zero, Has: &flags{ID: true}}, {Has: &flags{Name: true}}}))
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"id": int64(0)}, {"name": nil}}
	if !reflect.DeepEqual([]map[string]any(rows), want) {
		t.Fatalf("presence: %#v", rows)
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, err := Clone([]map[string]any{cycle}); err == nil {
		t.Fatal("cycle accepted")
	}
}
func TestCancellationAndSourceValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inline().Load(ctx, Maps(nil)); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := Inline().Load(context.Background(), Source{Kind: "maps", Data: []map[string]any{}, Path: "x"}); err == nil {
		t.Fatal("multiple sources accepted")
	}
	rows, err := Inline().Load(context.Background(), Maps(nil))
	if err != nil || len(rows) != 0 {
		t.Fatalf("explicit empty maps: %v %v", rows, err)
	}
}
