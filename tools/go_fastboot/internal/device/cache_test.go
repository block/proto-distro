package device

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCacheWorkingDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.Mkdir(filepath.Join(home, configDirName), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, configDirName, targetFileName)
	legacyData := []byte(`{"ip":"192.0.2.99"}`)
	if err := os.WriteFile(legacy, legacyData, 0600); err != nil {
		t.Fatal(err)
	}
	var caches []*Cache
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		t.Chdir(t.TempDir())
		cache, err := NewCache()
		if err != nil {
			t.Fatal(err)
		}
		if got, err := cache.LoadTarget(); err != nil || got != nil {
			t.Fatalf("new working directory inherited target: %+v, %v", got, err)
		}
		if err := cache.SaveTarget(&Device{IP: ip}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(configDirName, targetFileName)); err != nil {
			t.Fatal(err)
		}
		caches = append(caches, cache)
	}
	for i, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		got, err := caches[i].LoadTarget()
		if err != nil || !reflect.DeepEqual(got, &Device{IP: ip}) {
			t.Fatalf("directory %d target: %+v, %v", i, got, err)
		}
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != string(legacyData) {
		t.Fatalf("home cache changed: %q, %v", got, err)
	}
}
