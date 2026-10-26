package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Run in a disposable injector container with INJECTOR_INTEGRATION_TEST=1.
func TestInstallPackagesResolvesLocalDependencies(t *testing.T) {
	if os.Getenv("INJECTOR_INTEGRATION_TEST") != "1" {
		t.Skip("requires a disposable injector container with opkg")
	}
	for _, dir := range []string{packagesDir, rootfsMount} {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("integration test requires empty %s", dir)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeOpkgConfig(); err != nil {
		t.Fatal(err)
	}
	// Dependents sort before their dependencies; all are absent from the base rootfs.
	writeDependencyIPK(t, "flotilla-agent", "mdk-server")
	writeDependencyIPK(t, "mdk-server", "z-runtime")
	writeDependencyIPK(t, "z-runtime", "")
	installed, err := installPackages()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range installed {
		data, err := os.ReadFile(filepath.Join(rootfsMount, "usr/share", name))
		if err != nil || string(data) != name {
			t.Fatalf("installed payload for %s = %q, %v", name, data, err)
		}
	}
	if err := runDeferredPostinstalls(installed); err != nil {
		t.Fatal(err)
	}
	for _, name := range installed {
		data, err := os.ReadFile(filepath.Join(rootfsMount, "configured-"+name))
		if err != nil || string(data) != name+"\n" {
			t.Fatalf("postinstall result for %s = %q, %v", name, data, err)
		}
	}
	status, err := os.ReadFile(filepath.Join(rootfsMount, "var/lib/opkg/status"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range installed {
		if !strings.Contains(string(status), "Package: "+name+"\n") {
			t.Fatalf("package %s absent from opkg status", name)
		}
	}
}

func writeDependencyIPK(t *testing.T, name, dependency string) {
	t.Helper()
	control := fmt.Sprintf("Package: %s\nVersion: 1.0.0\nArchitecture: all\nMaintainer: Test\nDescription: Dependency regression fixture\n", name)
	if dependency != "" {
		control += "Depends: " + dependency + "\n"
	}
	postinst := "#!/bin/sh\nset -eu\n"
	if dependency != "" {
		postinst += fmt.Sprintf("test -f \"$OFFLINE_ROOT/configured-%s\"\n", dependency)
	}
	postinst += fmt.Sprintf("echo %s >> \"$OFFLINE_ROOT/configured-%s\"\n", name, name)
	controlArchive := dependencyTarGzip(t, map[string][]byte{
		"./control":  []byte(control),
		"./postinst": []byte(postinst),
	})
	dataArchive := dependencyTarGzip(t, map[string][]byte{"./usr/share/" + name: []byte(name)})
	ipk := dependencyTarGzip(t, map[string][]byte{
		"./debian-binary":  []byte("2.0\n"),
		"./control.tar.gz": controlArchive,
		"./data.tar.gz":    dataArchive,
	})
	if err := os.WriteFile(filepath.Join(packagesDir, name+"_all_1.0.0.ipk"), ipk, 0644); err != nil {
		t.Fatal(err)
	}
}

func dependencyTarGzip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
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
