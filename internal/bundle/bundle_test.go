package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eftakhairul/uc/internal/store"
)

// entry describes one hand-made tar entry.
type entry struct {
	name     string
	body     string
	typeflag byte
	linkname string
	size     int64 // overrides len(body) in the header when > 0
}

// makeTar builds a tar.gz from hand-made entries, for archives Write would
// never produce.
func makeTar(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		size := int64(len(e.body))
		if tf != tar.TypeReg {
			size = 0
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: size, Typeflag: tf, Linkname: e.linkname, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sample() *Bundle {
	f := store.NewFile()
	f.Aliases["gs"] = store.Alias{Command: "git status", Desc: "short status"}
	f.Functions["ship"] = store.Function{Body: "uc build && uc deploy", Examples: []string{"ship"}}
	return &Bundle{
		Scripts:  map[string][]byte{"killport": []byte("#!/bin/sh\necho kill\n"), "hello.py": []byte("print('hi')\n")},
		Commands: f,
	}
}

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	want := sample()
	if len(got.Scripts) != 2 || string(got.Scripts["killport"]) != string(want.Scripts["killport"]) {
		t.Errorf("scripts = %v", got.Scripts)
	}
	if !aliasEqual(got.Commands.Aliases["gs"], want.Commands.Aliases["gs"]) {
		t.Errorf("alias = %+v", got.Commands.Aliases["gs"])
	}
	if !functionEqual(got.Commands.Functions["ship"], want.Commands.Functions["ship"]) {
		t.Errorf("function = %+v", got.Commands.Functions["ship"])
	}
	if s, a, f := got.Counts(); s != 2 || a != 1 || f != 1 {
		t.Errorf("counts = %d %d %d", s, a, f)
	}
}

func TestWriteLayout(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	gz, _ := gzip.NewReader(&buf)
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if hdr.Uid != 0 || hdr.Gid != 0 {
			t.Errorf("%s: owner %d:%d", hdr.Name, hdr.Uid, hdr.Gid)
		}
	}
	if strings.Join(names, ",") != "commands.json,scripts/,scripts/hello.py,scripts/killport" {
		t.Errorf("entries = %v", names)
	}
}

func readErr(t *testing.T, data []byte) string {
	t.Helper()
	_, err := Read(bytes.NewReader(data))
	if err == nil {
		t.Fatal("want the archive rejected")
	}
	if _, ok := errors.AsType[*ImportError](err); !ok {
		t.Fatalf("err = %T %v, want *ImportError", err, err)
	}
	return err.Error()
}

func TestReadRejects(t *testing.T) {
	big := strings.Repeat("x", MaxFileBytes+1)
	okScript := entry{name: "scripts/ok", body: "#!/bin/sh\n"}
	for name, c := range map[string]struct {
		data []byte
		want string
	}{
		"not gzip":         {[]byte("plain text"), "cannot import: not a uc export file"},
		"gzip not tar":     {gzipOf(t, "not a tar archive at all, just text that is long enough"), "cannot import: not a uc export file"},
		"symlink":          {makeTar(t, entry{name: "scripts/x", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"}), "cannot import: 'scripts/x' is not a regular file"},
		"hard link":        {makeTar(t, okScript, entry{name: "scripts/y", typeflag: tar.TypeLink, linkname: "scripts/ok"}), "cannot import: 'scripts/y' is not a regular file"},
		"device":           {makeTar(t, entry{name: "scripts/dev", typeflag: tar.TypeChar}), "cannot import: 'scripts/dev' is not a regular file"},
		"fifo":             {makeTar(t, entry{name: "scripts/fifo", typeflag: tar.TypeFifo}), "cannot import: 'scripts/fifo' is not a regular file"},
		"traversal":        {makeTar(t, entry{name: "scripts/../../.bashrc", body: "x"}), "cannot import: unexpected entry 'scripts/../../.bashrc'"},
		"dotdot top":       {makeTar(t, entry{name: "../x", body: "x"}), "cannot import: unexpected entry '../x'"},
		"absolute":         {makeTar(t, entry{name: "/etc/cron.d/x", body: "x"}), "cannot import: unexpected entry '/etc/cron.d/x'"},
		"backslash":        {makeTar(t, entry{name: `scripts/..\x`, body: "x"}), `cannot import: unexpected entry 'scripts/..\x'`},
		"nested dir":       {makeTar(t, entry{name: "scripts/sub/x", body: "x"}), "cannot import: unexpected entry 'scripts/sub/x'"},
		"other dir":        {makeTar(t, entry{name: "other/", typeflag: tar.TypeDir}), "cannot import: unexpected entry 'other/'"},
		"stray file":       {makeTar(t, entry{name: "settings.json", body: "{}"}), "cannot import: unexpected entry 'settings.json'"},
		"duplicate":        {makeTar(t, okScript, okScript), "cannot import: 'scripts/ok' appears twice"},
		"bad script name":  {makeTar(t, entry{name: "scripts/.hidden", body: "x"}), "cannot import: invalid script name '.hidden'"},
		"reserved script":  {makeTar(t, entry{name: "scripts/list.sh", body: "x"}), "cannot import: invalid script name 'list.sh'"},
		"bad alias name":   {makeTar(t, entry{name: "commands.json", body: `{"aliases": {"a b": {"command": "x"}}}`}), "cannot import: invalid name 'a b' in commands.json"},
		"bad func name":    {makeTar(t, entry{name: "commands.json", body: `{"functions": {"-x": {"body": "x"}}}`}), "cannot import: invalid name '-x' in commands.json"},
		"empty alias":      {makeTar(t, entry{name: "commands.json", body: `{"aliases": {"e": {"command": "  "}}}`}), "cannot import: 'e' has an empty command"},
		"empty function":   {makeTar(t, entry{name: "commands.json", body: `{"functions": {"e": {"body": ""}}}`}), "cannot import: 'e' has an empty command"},
		"both kinds":       {makeTar(t, entry{name: "commands.json", body: `{"aliases": {"x": {"command": "a"}}, "functions": {"x": {"body": "b"}}}`}), "cannot import: 'x' is both an alias and a function"},
		"too big":          {makeTar(t, entry{name: "scripts/big", body: big}), "cannot import: 'scripts/big' is larger than 1 MB"},
		"newer version":    {makeTar(t, entry{name: "commands.json", body: `{"version": 9}`}), "cannot import: written by a newer uc — upgrade uc"},
		"bad json":         {makeTar(t, entry{name: "commands.json", body: "{\n,"}), "cannot import: commands.json is not valid: invalid JSON at line 2"},
		"empty":            {makeTar(t), "cannot import: the archive is empty"},
		"empty commands":   {makeTar(t, entry{name: "commands.json", body: `{"version": 1}`}), "cannot import: the archive is empty"},
		"ambiguous script": {makeTar(t, entry{name: "scripts/deploy.sh", body: "a"}, entry{name: "scripts/deploy.py", body: "b"}), "cannot import: 'deploy.py' and 'deploy.sh' would both run as 'deploy'"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := readErr(t, c.data); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func gzipOf(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte(s))
	_ = gz.Close()
	return buf.Bytes()
}

func TestReadTooManyEntries(t *testing.T) {
	entries := make([]entry, MaxEntries+1)
	for i := range entries {
		entries[i] = entry{name: fmt.Sprintf("scripts/s%04d", i), body: "x"}
	}
	if got := readErr(t, makeTar(t, entries...)); got != "cannot import: too many entries" {
		t.Errorf("got %q", got)
	}
}

// A highly compressible archive must be cut off by the size budget rather
// than buffered whole.
func TestReadBomb(t *testing.T) {
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	chunk := make([]byte, MaxFileBytes)
	for i := 0; i < 60; i++ {
		_ = tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("scripts/s%04d", i), Mode: 0o644, Size: MaxFileBytes, Typeflag: tar.TypeReg})
		_, _ = tw.Write(chunk)
	}
	_ = tw.Close()
	_ = gz.Close()
	if buf.Len() > 1<<20 {
		t.Fatalf("bomb fixture is %d bytes compressed; expected it to be small", buf.Len())
	}
	if got := readErr(t, buf.Bytes()); got != "cannot import: archive is too large" && got != "cannot import: 'scripts/s0000' is larger than 1 MB" {
		t.Errorf("got %q", got)
	}
}

// Just under the per-file limit, many times over: only the total budget
// can stop it.
func TestReadTotalBudget(t *testing.T) {
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	tw := tar.NewWriter(gz)
	chunk := make([]byte, MaxFileBytes)
	for i := 0; i < 55; i++ {
		_ = tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("scripts/s%04d", i), Mode: 0o644, Size: int64(len(chunk)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(chunk)
	}
	_ = tw.Close()
	_ = gz.Close()
	if got := readErr(t, buf.Bytes()); got != "cannot import: archive is too large" {
		t.Errorf("got %q", got)
	}
}

func TestReadCleansMetadata(t *testing.T) {
	data := makeTar(t, entry{name: "commands.json", body: `{"aliases": {"gs": {"command": "git status", "desc": "\u001b[31mred\u001b[0m"}}}`})
	b, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Commands.Aliases["gs"].Desc; got != "[31mred[0m" {
		t.Errorf("desc = %q", got)
	}
}

func TestPlan(t *testing.T) {
	local := &Bundle{
		Scripts: map[string][]byte{"same": []byte("s"), "changed": []byte("old"), "deploy.py": []byte("py")},
		Commands: &store.File{
			Aliases:   map[string]store.Alias{"gs": {Command: "git status"}, "edit": {Command: "old"}, "kind": {Command: "x"}},
			Functions: map[string]store.Function{"f": {Body: "b"}},
		},
	}
	incoming := &Bundle{
		Scripts: map[string][]byte{"same": []byte("s"), "changed": []byte("new"), "deploy.sh": []byte("sh"), "brand-new": []byte("n")},
		Commands: &store.File{
			Aliases:   map[string]store.Alias{"gs": {Command: "git status"}, "edit": {Command: "new"}, "fresh": {Command: "y"}},
			Functions: map[string]store.Function{"f": {Body: "b"}, "kind": {Body: "z"}},
		},
	}
	changes, same := Plan(incoming, local)
	var got []string
	for _, c := range changes {
		got = append(got, c.String())
	}
	want := []string{"+ script brand-new", "~ script changed", "~ script deploy.sh", "~ alias edit", "+ alias fresh", "~ function kind"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("changes = %q\nwant      %q", got, want)
	}
	if same != 3 {
		t.Errorf("same = %d, want 3 (script same, alias gs, function f)", same)
	}
}

func TestFromLocalSkipsWhatImportWouldRefuse(t *testing.T) {
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scripts, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"good":      "#!/bin/sh\n",
		"bad name":  "#!/bin/sh\n",
		".DS_Store": "junk",
		"huge":      strings.Repeat("x", MaxFileBytes+1),
	} {
		if err := os.WriteFile(filepath.Join(scripts, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	st := store.New(filepath.Join(dir, "commands.json"), "", "")
	st.Warn = io.Discard
	if err := os.WriteFile(st.Path, []byte(`{"aliases": {"ok": {"command": "x"}, "blank": {"command": ""}, "dup": {"command": "a"}}, "functions": {"dup": {"body": "b"}, "fn": {"body": "c"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	b, skipped, err := FromLocal(scripts, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Scripts) != 1 || b.Scripts["good"] == nil {
		t.Errorf("scripts = %v", b.Scripts)
	}
	if b.Commands.Kind("ok") != "alias" || b.Commands.Kind("fn") != "function" || b.Commands.Kind("blank") != "" || b.Commands.Kind("dup") != "alias" {
		t.Errorf("commands = %+v", b.Commands)
	}
	if len(skipped) != 4 {
		t.Errorf("skipped = %q, want 4", skipped)
	}
	// Whatever FromLocal returns must survive Read.
	var buf bytes.Buffer
	if err := Write(&buf, b); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(&buf); err != nil {
		t.Errorf("exported archive is not importable: %v", err)
	}
}

func TestApply(t *testing.T) {
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scripts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "deploy.py"), []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "commands.json"), "", "")
	st.Warn = io.Discard
	if err := st.SetFunction("kind", store.Function{Body: "was a function"}); err != nil {
		t.Fatal(err)
	}
	incoming := &Bundle{
		Scripts:  map[string][]byte{"deploy.sh": []byte("new")},
		Commands: &store.File{Aliases: map[string]store.Alias{"kind": {Command: "now an alias"}}, Functions: map[string]store.Function{}},
	}
	changes := []Change{{'~', "script", "deploy.sh"}, {'~', "alias", "kind"}}
	install := func(dest string, src io.Reader) error {
		data, _ := io.ReadAll(src)
		return os.WriteFile(dest, data, 0o700)
	}
	if err := Apply(incoming, changes, scripts, st, install); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(scripts, "deploy.py")); !os.IsNotExist(err) {
		t.Error("the other-extension script was kept, leaving 'deploy' ambiguous")
	}
	if data, _ := os.ReadFile(filepath.Join(scripts, "deploy.sh")); string(data) != "new" {
		t.Errorf("deploy.sh = %q", data)
	}
	f, _ := st.Load()
	if f.Kind("kind") != "alias" || f.Aliases["kind"].Command != "now an alias" {
		t.Errorf("store = %+v", f)
	}
}
