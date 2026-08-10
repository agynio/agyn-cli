package cmd

import (
	"strings"
	"testing"
)

// The picker returns a position and the caller switches on a constant, so the
// two have to agree. They disagree silently and in the worst direction: a row
// added in the wrong place installs a CA on the machine of someone who chose to
// skip, and nothing in the output would say so.
func TestCAChoiceItemsMatchTheirConstants(t *testing.T) {
	items := caChoiceItems()
	if len(items) != 3 {
		t.Fatalf("expected three answers, got %d", len(items))
	}
	for _, tc := range []struct {
		choice caChoice
		want   string
	}{
		{caInstall, "Install"},
		{caExport, "Export"},
		{caSkip, "Skip"},
	} {
		if !strings.HasPrefix(items[tc.choice].Label, tc.want) {
			t.Errorf("choice %d is %q, want it to start with %q", tc.choice, items[tc.choice].Label, tc.want)
		}
	}
}

// The default answer is the one that leaves the platform usable in a browser.
func TestCAChoiceOpensOnInstall(t *testing.T) {
	items := caChoiceItems()
	for index, item := range items {
		if item.Current && caChoice(index) != caInstall {
			t.Fatalf("expected the picker to open on install, opens on %q", item.Label)
		}
	}
	if !items[caInstall].Current {
		t.Fatal("expected install to be marked as the current answer")
	}
}
