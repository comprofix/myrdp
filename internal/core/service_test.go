package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"myrdp/internal/model"
	"myrdp/internal/secrets"
	"myrdp/internal/store"
)

func newService(t *testing.T) (*Service, string, *secrets.Memory) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connections.json")
	sec := secrets.NewMemory()
	s, err := New(store.NewJSONFile(path), sec)
	if err != nil {
		t.Fatal(err)
	}
	return s, path, sec
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func fieldOf(t *testing.T, s *Service, id, key string) FieldValue {
	t.Helper()
	ns, err := s.Settings(id)
	must(t, err)
	for _, f := range ns.Fields {
		if f.Field.Key == key {
			return f
		}
	}
	t.Fatalf("no field %s", key)
	return FieldValue{}
}

func TestInheritanceByOmission(t *testing.T) {
	s, path, _ := newService(t)
	folder, err := s.Create("", "folder", "Production")
	must(t, err)
	conn, err := s.Create(folder.ID, "connection", "SQL01")
	must(t, err)
	must(t, s.Set(conn.ID, "host", "sql01.example"))

	if f := fieldOf(t, s, conn.ID, "rdp.port"); f.Value != 3389 || f.Overridden || f.Source != "built-in default" {
		t.Fatalf("built-in default: %+v", f)
	}
	must(t, s.Set(model.RootID, "rdp.port", 3390))
	if f := fieldOf(t, s, conn.ID, "rdp.port"); f.Value != 3390 || f.Source != "Defaults" {
		t.Fatalf("document default: %+v", f)
	}
	must(t, s.Set(folder.ID, "rdp.port", "3391")) // strings from the UI are coerced
	if f := fieldOf(t, s, conn.ID, "rdp.port"); f.Value != 3391 || f.Source != "Production" {
		t.Fatalf("folder value: %+v", f)
	}
	must(t, s.Set(conn.ID, "rdp.port", 3392))
	if f := fieldOf(t, s, conn.ID, "rdp.port"); f.Value != 3392 || !f.Overridden {
		t.Fatalf("own value: %+v", f)
	}
	must(t, s.Reset(conn.ID, "rdp.port"))
	if f := fieldOf(t, s, conn.ID, "rdp.port"); f.Value != 3391 || f.Overridden {
		t.Fatalf("after reset: %+v", f)
	}

	// Only overrides are written to the file.
	data, err := os.ReadFile(path)
	must(t, err)
	var file struct {
		Defaults map[string]any `json:"defaults"`
		Tree     []struct {
			Name     string
			Rdp      map[string]any
			Children []map[string]any
		} `json:"tree"`
	}
	must(t, json.Unmarshal(data, &file))
	if file.Defaults["rdp"].(map[string]any)["port"] != 3390.0 || file.Tree[0].Rdp["port"] != 3391.0 {
		t.Fatalf("file contents:\n%s", data)
	}
	child := file.Tree[0].Children[0]
	if _, has := child["rdp"]; has || child["host"] != "sql01.example" {
		t.Fatalf("connection should store only its host:\n%s", data)
	}
}

func TestValidation(t *testing.T) {
	s, _, _ := newService(t)
	folder, _ := s.Create("", "folder", "F")
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"rdp.port", "abc"},
		{"rdp.resolution", "bogus"},
		{"nope", 1},
		{"host", "x"}, // connection-only
		{model.KeyPassword, true},
	} {
		if err := s.Set(folder.ID, tc.key, tc.value); err == nil {
			t.Errorf("Set(%s, %v) should fail", tc.key, tc.value)
		}
	}
}

func TestPasswordsStayOutOfTheFile(t *testing.T) {
	s, path, sec := newService(t)
	folder, _ := s.Create("", "folder", "Prod")
	conn, _ := s.Create(folder.ID, "connection", "SQL01")
	must(t, s.Set(conn.ID, "host", "sql01"))
	must(t, s.Set(folder.ID, "username", "matt"))
	must(t, s.SetSecret(folder.ID, model.KeyPassword, "s3cret!"))

	cfg, err := s.SessionConfig(conn.ID)
	must(t, err)
	if cfg.Username != "matt" || cfg.Password != "s3cret!" {
		t.Fatalf("inherited credentials: %+v", cfg)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "s3cret!") {
		t.Fatal("password written to connections file")
	}

	// A connection's own password wins over the folder's.
	must(t, s.SetSecret(conn.ID, model.KeyPassword, "own"))
	if cfg, _ := s.SessionConfig(conn.ID); cfg.Password != "own" {
		t.Fatalf("own password: %q", cfg.Password)
	}
	must(t, s.Reset(conn.ID, model.KeyPassword))
	if cfg, _ := s.SessionConfig(conn.ID); cfg.Password != "s3cret!" {
		t.Fatalf("after reset: %q", cfg.Password)
	}
	if v, _ := sec.Get(secrets.Key(conn.ID, model.KeyPassword)); v != "" {
		t.Fatal("reset left the secret in the keyring")
	}

	must(t, s.Delete(folder.ID))
	if v, _ := sec.Get(secrets.Key(folder.ID, model.KeyPassword)); v != "" {
		t.Fatal("delete left the secret in the keyring")
	}
}

func TestSessionConfig(t *testing.T) {
	s, _, _ := newService(t)
	conn, _ := s.Create("", "connection", "WEB")
	if _, err := s.SessionConfig(conn.ID); err == nil {
		t.Fatal("expected an error for a connection with no host")
	}
	must(t, s.Set(conn.ID, "host", "web01"))
	must(t, s.Set(conn.ID, "rdp.resolution", "1920x1080"))
	must(t, s.Set(conn.ID, "rdp.colorDepth", "24"))
	cfg, err := s.SessionConfig(conn.ID)
	must(t, err)
	if cfg.Width != 1920 || cfg.Height != 1080 || cfg.ColorDepth != 24 || cfg.Port != 3389 || !cfg.NLA || cfg.Name != "WEB" {
		t.Fatalf("%+v", cfg)
	}
	q, err := s.QuickConfig("10.0.0.5:3390")
	must(t, err)
	if q.Host != "10.0.0.5" || q.Port != 3390 {
		t.Fatalf("quick: %+v", q)
	}
}

func TestMoveDuplicateAndReload(t *testing.T) {
	s, path, sec := newService(t)
	a, _ := s.Create("", "folder", "A")
	b, _ := s.Create("", "folder", "B")
	c1, _ := s.Create(a.ID, "connection", "one")
	c2, _ := s.Create(a.ID, "connection", "two")
	c3, _ := s.Create(c1.ID, "connection", "three") // sibling after "one"

	names := func(nodes []*TreeNode) string {
		var out []string
		for _, n := range nodes {
			out = append(out, n.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(s.Tree()[0].Children); got != "one,three,two" {
		t.Fatalf("create order: %s", got)
	}
	must(t, s.Move(c1.ID, a.ID, 3)) // to the end of its own folder
	if got := names(s.Tree()[0].Children); got != "three,two,one" {
		t.Fatalf("reorder: %s", got)
	}
	must(t, s.Move(c2.ID, b.ID, -1))
	if err := s.Move(a.ID, a.ID, 0); err == nil {
		t.Fatal("moving a folder into itself should fail")
	}
	if err := s.Move(a.ID, c3.ID, 0); err == nil {
		t.Fatal("moving into a connection should fail")
	}

	must(t, s.SetSecret(c1.ID, model.KeyPassword, "pw"))
	dup, err := s.Duplicate(c1.ID)
	must(t, err)
	if v, _ := sec.Get(secrets.Key(dup.ID, model.KeyPassword)); v != "pw" {
		t.Fatal("duplicate did not copy the password")
	}

	// Reload from disk and compare the tree.
	want, _ := json.Marshal(s.Tree())
	s2, err := New(store.NewJSONFile(path), sec)
	must(t, err)
	got, _ := json.Marshal(s2.Tree())
	if string(want) != string(got) {
		t.Fatalf("reload mismatch\nwant %s\ngot  %s", want, got)
	}
	if id := s2.FindByName("ONE (copy)"); id != dup.ID {
		t.Fatalf("FindByName: %q", id)
	}
}

func TestUnknownFieldsSurviveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connections.json")
	in := `{"schemaVersion":1,"futureTopLevel":{"a":1},"defaults":{"ssh":{"port":22}},"credentials":[],
	"tree":[{"id":"n1","type":"connection","name":"X","host":"x","future":{"deep":{"flag":true}},"tags":["a","b"]}]}`
	must(t, os.WriteFile(path, []byte(in), 0o600))
	s, err := New(store.NewJSONFile(path), secrets.NewMemory())
	must(t, err)
	must(t, s.Rename("n1", "Y"))

	data, _ := os.ReadFile(path)
	var out map[string]any
	must(t, json.Unmarshal(data, &out))
	node := out["tree"].([]any)[0].(map[string]any)
	if out["futureTopLevel"].(map[string]any)["a"] != 1.0 ||
		out["defaults"].(map[string]any)["ssh"].(map[string]any)["port"] != 22.0 ||
		node["future"].(map[string]any)["deep"].(map[string]any)["flag"] != true ||
		len(node["tags"].([]any)) != 2 || node["name"] != "Y" {
		t.Fatalf("unknown fields lost:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); err != nil {
		t.Fatal("no startup backup was made")
	}

	// A file from a newer schema is refused rather than downgraded.
	must(t, os.WriteFile(path, []byte(`{"schemaVersion":99,"tree":[]}`), 0o600))
	if _, err := New(store.NewJSONFile(path), secrets.NewMemory()); err == nil {
		t.Fatal("expected newer-schema file to be refused")
	}
}

func TestSaveIsAllOrNothing(t *testing.T) {
	s, path, sec := newService(t)
	folder, _ := s.Create("", "folder", "Prod")

	// A draft shows what a new connection would inherit, and creates nothing.
	must(t, s.Set(folder.ID, "rdp.port", 3390))
	draft, err := s.DraftSettings(folder.ID, "connection")
	must(t, err)
	for _, f := range draft.Fields {
		if f.Field.Key == "rdp.port" && (f.Value != 3390 || f.Inherited != 3390 || f.Source != "Prod" || f.Overridden) {
			t.Fatalf("draft port: %+v", f)
		}
	}
	if draft.Parent != "Prod" || len(s.Tree()[0].Children) != 0 {
		t.Fatalf("draft should not create a node: %+v", s.Tree())
	}

	// One bad value rejects the whole save: no node, no keyring entry, no file change.
	before, _ := os.ReadFile(path)
	_, err = s.Save(SaveRequest{ParentID: folder.ID, Type: "connection", Name: "SQL01",
		Set: map[string]any{"host": "sql01", "rdp.port": "not a number"}, Secrets: map[string]string{model.KeyPassword: "pw"}})
	if err == nil {
		t.Fatal("expected the save to be rejected")
	}
	after, _ := os.ReadFile(path)
	if len(s.Tree()[0].Children) != 0 || string(before) != string(after) {
		t.Fatal("a rejected save changed something")
	}
	if _, err := s.Save(SaveRequest{ParentID: folder.ID, Type: "connection"}); err == nil {
		t.Fatal("a new node needs a name")
	}

	node, err := s.Save(SaveRequest{ParentID: folder.ID, Type: "connection", Name: "SQL01",
		Set: map[string]any{"host": "sql01", "rdp.nla": false}, Secrets: map[string]string{model.KeyPassword: "pw"}})
	must(t, err)
	cfg, err := s.SessionConfig(node.ID)
	must(t, err)
	if cfg.Host != "sql01" || cfg.NLA || cfg.Port != 3390 || cfg.Password != "pw" {
		t.Fatalf("created connection: %+v", cfg)
	}

	// An edit can rename, set, reset and drop the password in one go.
	_, err = s.Save(SaveRequest{ID: node.ID, Name: "SQL01b", Set: map[string]any{"rdp.console": true},
		Reset: []string{"rdp.nla", model.KeyPassword}})
	must(t, err)
	cfg, _ = s.SessionConfig(node.ID)
	if cfg.Name != "SQL01b" || !cfg.NLA || !cfg.Console || cfg.Password != "" {
		t.Fatalf("edited connection: %+v", cfg)
	}
	if v, _ := sec.Get(secrets.Key(node.ID, model.KeyPassword)); v != "" {
		t.Fatal("reset left the password in the keyring")
	}
}
