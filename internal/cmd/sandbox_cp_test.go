package cmd

import "testing"

// Exactly one side carries NAME:path — the docker cp and kubectl cp convention.
// A local path holding a colon must not be mistaken for a sandbox.
func TestSplitRemoteRecognizesOnlyASandbox(t *testing.T) {
	for _, tc := range []struct {
		value  string
		remote bool
		name   string
		path   string
	}{
		{value: "brave-otter:/workspace/out.csv", remote: true, name: "brave-otter", path: "/workspace/out.csv"},
		{value: "brave-otter:", remote: true, name: "brave-otter", path: "/workspace"},
		{value: "./report.pdf", remote: false},
		{value: "/tmp/a:b", remote: false},
		{value: "../rel:x", remote: false},
		{value: ":/workspace", remote: false},
	} {
		spec, ok := splitRemote(tc.value)
		if ok != tc.remote {
			t.Fatalf("%q: remote=%v, want %v", tc.value, ok, tc.remote)
		}
		if !ok {
			continue
		}
		if spec.name != tc.name || spec.path != tc.path {
			t.Fatalf("%q: got %+v, want name=%s path=%s", tc.value, spec, tc.name, tc.path)
		}
	}
}

// The endpoint serves a root and addresses everything relative to it, so a copy
// splits the workspace off from the path inside it.
func TestSplitRootSeparatesTheServedDirectory(t *testing.T) {
	for _, tc := range []struct{ in, root, rel string }{
		{in: "/workspace/out.csv", root: "/workspace", rel: "out.csv"},
		{in: "/workspace/nested/deep.txt", root: "/workspace", rel: "nested/deep.txt"},
		{in: "/workspace", root: "/workspace", rel: ""},
		{in: "/workspace/", root: "/workspace", rel: ""},
		// Outside the workspace the endpoint will refuse it; serving the parent
		// keeps that refusal meaningful rather than turning it into a scan of /.
		{in: "/etc/passwd", root: "/etc", rel: "passwd"},
	} {
		root, rel := splitRoot(tc.in)
		if root != tc.root || rel != tc.rel {
			t.Fatalf("%q: got root=%q rel=%q, want root=%q rel=%q", tc.in, root, rel, tc.root, tc.rel)
		}
	}
}
