package local

import "testing"

// Each tool prints its version differently and all three put it in the first
// line. A parser that only handled one of them would silently treat the others
// as unknown, which is the state that never blocks.
func TestParseVersionReadsWhatEachToolActuallyPrints(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"limactl version 1.0.4", "1.0.4"},
		{"xz (XZ Utils) 5.6.2", "5.6.2"},
		{"QEMU emulator version 9.1.0", "9.1.0"},
		{"limactl version 2.1.3", "2.1.3"},
		{"some tool with no version", ""},
	} {
		if got := parseVersion(tc.line); got != tc.want {
			t.Errorf("parseVersion(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.0.4", "1.0.0", 1},
		{"0.23.0", "1.0.0", -1},
		{"1.0", "1.0.0", 0},
		{"11.0.1", "9.1.0", 1},
		// A tool that would not report a version is given the benefit of the
		// doubt: refusing to start over an unreadable --version would be worse
		// than the too-old case it is trying to catch.
		{"", "1.0.0", 0},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// A tool no package manager packages must still say how to get it, or the
// preflight reports a problem with no way out.
func TestEveryRequiredToolHasAFix(t *testing.T) {
	for _, tool := range RequiredTools() {
		if tool.fix() == "" {
			t.Errorf("%s has no fix", tool.Name)
		}
		if tool.MinVersion == "" {
			t.Errorf("%s has no minimum version", tool.Name)
		}
	}
}
