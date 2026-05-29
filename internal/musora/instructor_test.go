package musora

import (
	"strings"
	"testing"
)

// recorded shape of the `result` array returned by instructor_lessons.groq:
// rows with id/type/title, including one null id (a doc whose railcontent_id is
// null, e.g. an instructor doc) that must be dropped by the filter.
const instructorLessonsFixture = `[
  {"id": 409875, "type": "course", "title": "Independence Builder"},
  {"id": null, "type": "instructor", "title": "Aaron Edgar"},
  {"id": 409876, "type": "course-part", "title": "Part Two"},
  {"id": 0, "type": "song", "title": "Zero Id Bogus"}
]`

func TestParseLessonRefsDropsNullAndZeroIDs(t *testing.T) {
	refs, err := parseLessonRefs([]byte(instructorLessonsFixture))
	if err != nil {
		t.Fatalf("parseLessonRefs error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 refs after dropping null/zero ids, got %d: %#v", len(refs), refs)
	}
	got := map[int]string{}
	for _, r := range refs {
		got[r.ID] = r.Title
	}
	if got[409875] != "Independence Builder" {
		t.Fatalf("missing/incorrect 409875 row: %#v", refs)
	}
	if got[409876] != "Part Two" {
		t.Fatalf("missing/incorrect 409876 row: %#v", refs)
	}
	if _, ok := got[0]; ok {
		t.Fatalf("zero-id row was not dropped: %#v", refs)
	}
	// Type carried through.
	for _, r := range refs {
		if r.ID == 409875 && r.Type != "course" {
			t.Fatalf("type not parsed: %#v", r)
		}
	}
}

func TestParseLessonRefsEmpty(t *testing.T) {
	refs, err := parseLessonRefs([]byte(`[]`))
	if err != nil {
		t.Fatalf("parseLessonRefs error on empty: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("want 0 refs, got %d", len(refs))
	}
}

func TestValidateSlugRejectsInjection(t *testing.T) {
	bad := []string{
		"a'; b",                  // quote + space (GROQ injection)
		"aaron edgar",            // space
		"Aaron-Edgar",            // uppercase
		"slug'][0]._id)||true//", // bracket/quote injection
		"",                       // empty
		"slug\"quoted",           // double quote
		"name[0]",                // brackets
		"a/b",                    // slash
	}
	for _, s := range bad {
		if err := validateSlug(s); err == nil {
			t.Fatalf("validateSlug(%q) accepted an invalid slug", s)
		}
	}
	good := []string{"aaron-edgar", "jared-falk", "mike2", "a", "0", "drum-set-101"}
	for _, s := range good {
		if err := validateSlug(s); err != nil {
			t.Fatalf("validateSlug(%q) rejected a valid slug: %v", s, err)
		}
	}
}

func TestValidateBrandAllowlist(t *testing.T) {
	for _, b := range []string{"drumeo", "pianote", "guitareo", "singeo", "playbass"} {
		if err := validateBrand(b); err != nil {
			t.Fatalf("validateBrand(%q) rejected an allowed brand: %v", b, err)
		}
	}
	for _, b := range []string{"", "Drumeo", "evil'brand", "drumeo'][0]", "rockstar"} {
		if err := validateBrand(b); err == nil {
			t.Fatalf("validateBrand(%q) accepted a disallowed brand", b)
		}
	}
}

func TestInstructorLessonsValidatesBeforeNetwork(t *testing.T) {
	// Invalid slug/brand must error out before any network call; assert we get
	// an error and no panic (network never reached because validation precedes it).
	if _, err := InstructorLessons("a'; DROP", "drumeo", "92"); err == nil {
		t.Fatalf("InstructorLessons accepted an invalid slug")
	}
	if _, err := InstructorLessons("aaron-edgar", "evil'brand", "92"); err == nil {
		t.Fatalf("InstructorLessons accepted an invalid brand")
	}
}

func TestInstructorTemplateSubstitution(t *testing.T) {
	// The substituted template must contain the validated slug+brand and must
	// still carry the array::intersects(...,[92]) clause so ApplyPermissions can
	// rewrite the permission list. Sentinels must be fully replaced.
	tpl, err := buildInstructorQuery("aaron-edgar", "drumeo")
	if err != nil {
		t.Fatalf("buildInstructorQuery error: %v", err)
	}
	if strings.Contains(tpl, "$SLUG$") || strings.Contains(tpl, "$BRAND$") {
		t.Fatalf("sentinels not fully replaced: %s", tpl)
	}
	if !strings.Contains(tpl, "slug.current=='aaron-edgar'") {
		t.Fatalf("slug not substituted: %s", tpl)
	}
	if !strings.Contains(tpl, "brand=='drumeo'") {
		t.Fatalf("brand not substituted: %s", tpl)
	}
	if !strings.Contains(tpl, "array::intersects(permission_v2,[92])") {
		t.Fatalf("permission clause missing (ApplyPermissions would not match): %s", tpl)
	}
	// And the permission clause is actually rewritten by ApplyPermissions.
	rewritten := ApplyPermissions(tpl, "92,100")
	if !strings.Contains(rewritten, "array::intersects(permission_v2,[92,100])") {
		t.Fatalf("ApplyPermissions did not rewrite the permission list: %s", rewritten)
	}
}
