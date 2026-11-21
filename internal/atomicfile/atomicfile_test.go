package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

func TestWriteCreatesPrivateDirAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "x.json")
	if err := Write(path, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hi" {
		t.Fatalf("read back %q, %v", data, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != DirMode {
		t.Errorf("dir mode = %o, want %o", info.Mode().Perm(), DirMode)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", info.Mode().Perm())
	}
}

// Without the lock, concurrent read-modify-write cycles lose updates;
// with it every increment survives.
func TestWithLockSerializesReadModifyWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counter")
	if err := Write(path, []byte("0")); err != nil {
		t.Fatal(err)
	}
	const n = 40
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			err := WithLock(path, func() error {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				v, _ := strconv.Atoi(string(data))
				return Write(path, []byte(strconv.Itoa(v+1)))
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	data, _ := os.ReadFile(path)
	if string(data) != strconv.Itoa(n) {
		t.Errorf("counter = %s, want %d", data, n)
	}
}

func TestWithLockReturnsFnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x")
	want := os.ErrInvalid
	if err := WithLock(path, func() error { return want }); err != want {
		t.Errorf("err = %v, want %v", err, want)
	}
}
