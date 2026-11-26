package cookies

import (
	"os"
	"path/filepath"
	"testing"
)

// A JSON array that holds no cookies is not a cookie jar: it is some other
// file of the user's. Load refuses it, so Save never replaces it. Before,
// ghola read it as an empty v0 jar and wrote cookies over it.
func TestLoad_RefusesAJSONFileThatIsNotAJar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	orig := []byte(`[{"important":"user data"}]`)
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := New(path)
	if err == nil {
		err = j.Load()
	}
	if err == nil {
		t.Fatal("Load accepted a JSON file that holds no cookies")
	}
	if b, _ := os.ReadFile(path); string(b) != string(orig) {
		t.Errorf("the file changed: %s", b)
	}
}
