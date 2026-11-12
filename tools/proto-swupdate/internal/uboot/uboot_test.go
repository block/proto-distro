package uboot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestActivePartitionRejectsInvalidDeviceNumber(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "findmnt"), []byte("#!/bin/sh\nprintf '%s\\n' \"$FINDMNT_OUTPUT\"\nexit \"${FINDMNT_EXIT:-0}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, output := range []string{"/dev/dm-0", "", "253", "253:", ":0", "+253:0", "253:-1", "253:0:1", "253:0\n179:7", "../../7"} {
		t.Run(output, func(t *testing.T) {
			t.Setenv("FINDMNT_OUTPUT", output)
			if got, err := ActivePartition(); err == nil || got != "" {
				t.Fatalf("ActivePartition() = %q, %v; want error", got, err)
			}
		})
	}
	t.Run("command failure", func(t *testing.T) {
		t.Setenv("FINDMNT_OUTPUT", "253:0")
		t.Setenv("FINDMNT_EXIT", "1")
		if got, err := ActivePartition(); err == nil || got != "" {
			t.Fatalf("ActivePartition() = %q, %v; want error", got, err)
		}
	})
}

func TestSetEnvsWritesOneScript(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FW_ARGS\"\ncat > \"$FW_STDIN\"\nexit \"${FW_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(dir, "fw_setenv"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("FW_ARGS", filepath.Join(dir, "args"))
	t.Setenv("FW_STDIN", filepath.Join(dir, "stdin"))

	if err := SetEnvs(map[string]string{"ustate": "0", "rootpart": "7"}); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"args": "-s -\n", "stdin": "rootpart=7\nustate=0\n"} {
		got, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("fw_setenv %s = %q, want %q", file, got, want)
		}
	}

	for name, vars := range map[string]map[string]string{
		"name with =":    {"a=b": "1"},
		"value with \\n": {"rootpart": "7\nustate=7"},
		"empty name":     {"": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := SetEnvs(vars); err == nil {
				t.Fatal("want error")
			}
		})
	}
	t.Run("fw_setenv failure", func(t *testing.T) {
		t.Setenv("FW_EXIT", "1")
		if err := SetEnvs(map[string]string{"ustate": "0"}); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestPartitionNumber(t *testing.T) {
	type testCase struct {
		name  string
		files map[string]string
		dirs  []string
		want  string
	}
	cases := []testCase{
		{name: "direct partition7", files: map[string]string{"partition": "7\n"}, want: "7"},
		{name: "direct partition8", files: map[string]string{"partition": "8\n"}, want: "8"},
		{name: "dm-0 partition7", files: map[string]string{"slaves/mmcblk0p7/partition": "7\n"}, want: "7"},
		{name: "dm-0 partition8", files: map[string]string{"slaves/mmcblk0p8/partition": "8\n"}, want: "8"},
		{name: "canonical decimal", files: map[string]string{"partition": "007\n"}, want: "7"},
		{name: "missing slaves"},
		{name: "zero slaves", dirs: []string{"slaves"}},
		{name: "multiple slaves", files: map[string]string{"slaves/mmcblk0p7/partition": "7\n", "slaves/mmcblk0p8/partition": "8\n"}},
		{name: "slave not partition", dirs: []string{"slaves/mmcblk0"}},
		{name: "nested mapping", files: map[string]string{"slaves/dm-1/slaves/mmcblk0p7/partition": "7\n"}},
		{name: "partition read error", dirs: []string{"partition"}, files: map[string]string{"slaves/mmcblk0p7/partition": "7\n"}},
		{name: "slaves read error", files: map[string]string{"slaves": "not a directory"}},
		{name: "slave partition read error", dirs: []string{"slaves/mmcblk0p7/partition"}},
	}
	for _, value := range []string{"", "0", "-7", "+7", "7x", "7\n8", "18446744073709551616"} {
		cases = append(cases, testCase{name: "invalid partition " + value, files: map[string]string{"partition": value, "slaves/mmcblk0p7/partition": "7\n"}})
		cases = append(cases, testCase{name: "invalid slave partition " + value, files: map[string]string{"slaves/mmcblk0p7/partition": value}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			device := filepath.Join(root, "devices", "dm-0")
			if err := os.MkdirAll(device, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, dir := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(device, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for name, value := range tc.files {
				path := filepath.Join(device, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// /sys/dev/block entries are symlinks to actual device directories.
			deviceLink := filepath.Join(root, "253:0")
			if err := os.Symlink(device, deviceLink); err != nil {
				t.Fatal(err)
			}
			got, err := partitionNumber(deviceLink)
			if tc.want == "" {
				if err == nil || got != "" {
					t.Fatalf("partitionNumber() = %q, %v; want error", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("partitionNumber() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
