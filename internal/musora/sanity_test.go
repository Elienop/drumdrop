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
