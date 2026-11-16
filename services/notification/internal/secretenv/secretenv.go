// NUSXA: shared/go/secretenv/secretenv.go (notification o'z build kontekstida quriladi va shared modulga bog'liq emas).
// O'zgartirsangiz — ikkalasini birga.
// Package secretenv — Docker secrets (F-16): SECRETS_DIR (default /run/secrets/izgo) papkasidagi har bir fayl
// shu nomli muhit o'zgaruvchisiga yuklanadi (fayl nomi = o'zgaruvchi nomi, masalan DB_PASSWORD, INTERNAL_TOKENS).
//
// Qoidalar (NestJS src/config/secret-env.ts va Spring configtree bilan bir xil):
//   - muhitda BO'SH BO'LMAGAN qiymat bo'lsa — u ustun (testlar va lokal ishga tushirish);
//   - papka yo'q — hech narsa qilinmaydi (testlar, .env bilan ishga tushirish);
//   - faqat ^[A-Z][A-Z0-9_]*$ nomli oddiy fayllar; symlink kuzatiladi (Kubernetes secret volume: NAME -> ..data/NAME),
//     papkalar va boshqa nomlar (..data, .hidden) o'tkazib yuboriladi;
//   - oxiridagi bitta \n yoki \r\n olib tashlanadi; 64 KiB dan katta fayl — xato.
//
// main() ning eng boshida, config o'qilishidan OLDIN chaqiriladi. Qiymatlar logga yozilmaydi, faqat nomlar.
package secretenv

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	DefaultDir = "/run/secrets/izgo"
	maxSize    = 64 << 10
)

var nameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// LoadDefault — SECRETS_DIR (bo'sh bo'lsa DefaultDir) dan yuklaydi.
func LoadDefault() ([]string, error) {
	dir := os.Getenv("SECRETS_DIR")
	if dir == "" {
		dir = DefaultDir
	}
	return Load(dir)
}

// Load — dir dagi fayllarni muhitga yuklaydi; yuklangan nomlarni (tartiblangan) qaytaradi.
func Load(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secretenv: %s: %w", dir, err)
	}
	var loaded []string
	for _, e := range entries {
		name := e.Name()
		if !nameRe.MatchString(name) {
			continue
		}
		if v, ok := os.LookupEnv(name); ok && v != "" {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path) // symlink kuzatiladi
		if err != nil {
			return nil, fmt.Errorf("secretenv: %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxSize {
			return nil, fmt.Errorf("secretenv: %s: %d bayt (chegara %d)", name, info.Size(), maxSize)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("secretenv: %s: %w", name, err)
		}
		v := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		if err := os.Setenv(name, v); err != nil {
			return nil, fmt.Errorf("secretenv: %s: %w", name, err)
		}
		loaded = append(loaded, name)
	}
	sort.Strings(loaded)
	return loaded, nil
}
