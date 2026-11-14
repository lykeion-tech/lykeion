package kernels

import "testing"

func TestNamespaceKernelPIDs(t *testing.T) {
	rows := []pidRecord{{pid: 10, parent: 1, namespace: "host", inner: 10, start: "outer"}, {pid: 40, parent: 10, namespace: "sandbox", inner: 1, start: "bridge"}, {pid: 70, parent: 40, namespace: "sandbox", inner: 2, start: "kernel"}, {pid: 2, parent: 1, namespace: "host", inner: 2, start: "unrelated"}, {pid: 80, parent: 1, namespace: "other", inner: 2, start: "other"}}
	got, bridge := resolvePIDs(rows, 10, "outer", 1, []int{2, 2, 999})
	if bridge != 40 || len(got) != 1 || got[0] != 70 {
		t.Fatalf("namespace mapping %v bridge%d", got, bridge)
	}
	rows[0].start = "reused"
	got, bridge = resolvePIDs(rows, 10, "outer", 1, []int{2})
	if len(got) != 0 || bridge != 0 {
		t.Fatalf("PID reuse accepted: %v %d", got, bridge)
	}
	rows[0].start = "outer"
	rows[2].parent = 1
	got, _ = resolvePIDs(rows, 10, "outer", 1, []int{2})
	if len(got) != 0 {
		t.Fatalf("unrelated PID accepted: %v", got)
	}
}

func TestNamespacePIDRequiresBridgeNamespaceAndAncestry(t *testing.T) {
	rows := []pidRecord{{pid: 10, parent: 1, namespace: "host", inner: 10, start: "outer"}, {pid: 40, parent: 10, namespace: "sandbox", inner: 1}, {pid: 70, parent: 40, namespace: "different", inner: 2}}
	got, bridge := resolvePIDs(rows, 10, "outer", 1, []int{2})
	if bridge != 40 || len(got) != 0 {
		t.Fatalf("namespace crossing %v %d", got, bridge)
	}
	rows = append(rows, pidRecord{pid: 80, parent: 40, namespace: "nested", inner: 1})
	got, bridge = resolvePIDs(rows, 10, "outer", 1, []int{2})
	if bridge != 0 || len(got) != 0 {
		t.Fatalf("ambiguous bridge %v %d", got, bridge)
	}
}
