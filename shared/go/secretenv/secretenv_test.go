package secretenv_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/izgo/shared/go/secretenv"
)

func write(t *testing.T, dir, name, v string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "IZGO_T_DB_PASSWORD", "s3cret\n")
	write(t, dir, "IZGO_T_CRLF", "a,b\r\n")
	write(t, dir, "IZGO_T_KEEP", "from-file")
	write(t, dir, "IZGO_T_EMPTYENV", "file-wins")
	write(t, dir, "lowercase", "x")
	write(t, dir, ".hidden", "x")
	if err := os.Mkdir(filepath.Join(dir, "IZGO_T_DIR"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "IZGO_T_KEEP"), filepath.Join(dir, "IZGO_T_LINK")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IZGO_T_KEEP", "from-env") // aniq berilgan env ustun
	t.Setenv("IZGO_T_EMPTYENV", "")     // bo'sh env — fayl ishlatiladi
	t.Setenv("IZGO_T_DB_PASSWORD", "")  // t.Setenv tozalashi uchun
	t.Setenv("IZGO_T_CRLF", "")
	t.Setenv("IZGO_T_LINK", "")

	got, err := secretenv.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"IZGO_T_CRLF", "IZGO_T_DB_PASSWORD", "IZGO_T_EMPTYENV", "IZGO_T_LINK"}
	if !slices.Equal(got, want) {
		t.Fatalf("yuklandi %v, kutilgan %v", got, want)
	}
	for k, v := range map[string]string{
		"IZGO_T_DB_PASSWORD": "s3cret", "IZGO_T_CRLF": "a,b", "IZGO_T_KEEP": "from-env", "IZGO_T_EMPTYENV": "file-wins",
	} {
		if os.Getenv(k) != v {
			t.Errorf("%s = %q, kutilgan %q", k, os.Getenv(k), v)
		}
	}
	if _, ok := os.LookupEnv("lowercase"); ok {
		t.Error("kichik harfli fayl yuklandi")
	}
	if os.Getenv("IZGO_T_LINK") != "from-file" { // k8s: NAME -> ..data/NAME
		t.Errorf("symlink: %q", os.Getenv("IZGO_T_LINK"))
	}
}

func TestLoadMissingDirAndLimits(t *testing.T) {
	if got, err := secretenv.Load(filepath.Join(t.TempDir(), "yoq")); err != nil || got != nil {
		t.Fatalf("papka yo'q: %v %v", got, err)
	}
	dir := t.TempDir()
	write(t, dir, "IZGO_T_BIG", strings.Repeat("x", 64<<10+1))
	t.Setenv("IZGO_T_BIG", "")
	if _, err := secretenv.Load(dir); err == nil || !strings.Contains(err.Error(), "IZGO_T_BIG") {
		t.Fatalf("katta fayl: %v", err)
	}
	t.Setenv("SECRETS_DIR", t.TempDir())
	if got, err := secretenv.LoadDefault(); err != nil || len(got) != 0 {
		t.Fatalf("LoadDefault: %v %v", got, err)
	}
}
