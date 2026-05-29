package musora

import (
	"strings"
	"testing"
)

func TestWithIDReplacesFirstFilterOnly(t *testing.T) {
	tpl := "*[railcontent_id == 409918 && status]{ 'p': parent_content_reference[0]->railcontent_id }"
	out := WithID(tpl, 123)
	if !strings.Contains(out, "railcontent_id == 123 && status") {
		t.Fatalf("first filter not replaced: %s", out)
	}
	if !strings.Contains(out, "parent_content_reference[0]->railcontent_id") {
		t.Fatalf("projection ref wrongly modified: %s", out)
	}
}

func TestWithIDReplacesOnlyFirstOfMultipleFilters(t *testing.T) {
	tpl := "a[railcontent_id == 1] b[railcontent_id == 2]"
	out := WithID(tpl, 99)
	if !strings.Contains(out, "a[railcontent_id == 99]") {
		t.Fatalf("first filter not replaced: %s", out)
	}
	if !strings.Contains(out, "b[railcontent_id == 2]") {
		t.Fatalf("second filter should be left untouched: %s", out)
	}
	if strings.Contains(out, "b[railcontent_id == 99]") {
		t.Fatalf("second filter was wrongly replaced: %s", out)
	}
}

func TestApplyPermissionsDefaultAndNoop(t *testing.T) {
	q := "x array::intersects(permission_v2, [92]) y"
	if got := ApplyPermissions(q, ""); got != q {
		t.Fatalf("default changed query: %s", got)
	}
	plain := "*[railcontent_id == 1]{title}"
	if got := ApplyPermissions(plain, ""); got != plain {
		t.Fatalf("non-permission query changed: %s", got)
	}
}

func TestApplyPermissionsValidatesIDs(t *testing.T) {
	q := "x array::intersects(permission_v2, [0]) y"

	// A valid comma-separated id list is substituted verbatim.
	if got := ApplyPermissions(q, "1,2,3"); got != "x array::intersects(permission_v2, [1,2,3]) y" {
		t.Fatalf("valid ids not substituted: %s", got)
	}

	// Malformed values must fall back to the default ("92") rather than inject
	// arbitrary text into the GROQ array literal.
	want := "x array::intersects(permission_v2, [92]) y"
	for _, bad := range []string{
		"92]) || true || ([", // injection attempt
		"abc",                // non-numeric
		"1,",                 // trailing comma
		",1",                 // leading comma
		"1, 2",               // embedded space
		"1;2",                // wrong separator
	} {
		if got := ApplyPermissions(q, bad); got != want {
			t.Fatalf("ApplyPermissions(%q) = %q, want fallback %q", bad, got, want)
		}
	}
}
