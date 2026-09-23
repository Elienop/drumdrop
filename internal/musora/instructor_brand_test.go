package musora_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
	"github.com/elienop/drumdrop/internal/musora/musoratest"
)

func lessonIDs(refs []musora.LessonRef) []int {
	ids := make([]int, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	return ids
}

// TestInstructorLessonsFindsTheBrandsLessons proves an instructor's lessons in
// a brand are found whichever of their documents they reference, when Musora
// files the instructor under one slug in several documents and the other
// brand's sorts first. This is the query every sync of an instructor follow
// runs (engine.Expander), from the follow's stored slug and brand.
func TestInstructorLessonsFindsTheBrandsLessons(t *testing.T) {
	for _, c := range []struct {
		brand string
		want  []int
	}{
		{"drumeo", []int{1001, 1002}},
		{"singeo", []int{2001, 2002}},
		{"pianote", []int{}},
	} {
		t.Run(c.brand, func(t *testing.T) {
			musoratest.Serve(t, musoratest.JaredFalk())
			refs, err := musora.InstructorLessons("jared-falk", c.brand, "")
			if err != nil {
				t.Fatalf("InstructorLessons: %v", err)
			}
			if got := lessonIDs(refs); !slices.Equal(got, c.want) {
				t.Errorf("lessons in %s = %v, want %v", c.brand, got, c.want)
			}
		})
	}
}

// TestResolveInstructorIDPicksTheBrandsDocument proves the name a follow
// stores is the instructor document's for the follow's brand, not the first
// one Musora answers, and that a brand with no document of its own still
// finds the instructor (its lessons may reference another brand's).
func TestResolveInstructorIDPicksTheBrandsDocument(t *testing.T) {
	for _, c := range []struct {
		brand, wantID, wantName string
	}{
		{"drumeo", "instructor_jaredfalk_31880", "Jared Falk"},
		{"singeo", "instructor_jaredfalk_314120", "Jared Falk (Singeo)"},
		{"pianote", "instructor_jaredfalk_314120", "Jared Falk (Singeo)"},
	} {
		t.Run(c.brand, func(t *testing.T) {
			musoratest.Serve(t, musoratest.JaredFalk())
			id, name, ok, err := musora.ResolveInstructorID("jared-falk", c.brand)
			if err != nil || !ok {
				t.Fatalf("ResolveInstructorID = ok %v, err %v; want found", ok, err)
			}
			if id != c.wantID || name != c.wantName {
				t.Errorf("in %s: got %s %q, want %s %q", c.brand, id, name, c.wantID, c.wantName)
			}
		})
	}
}

// TestResolveInstructorIDUnknownAndBadBrand proves an unknown slug is not
// found, without an error, and a brand Musora doesn't have is refused before
// Musora is asked.
func TestResolveInstructorIDUnknownAndBadBrand(t *testing.T) {
	queries := musoratest.Serve(t, musoratest.JaredFalk())
	if _, _, ok, err := musora.ResolveInstructorID("nobody", "drumeo"); ok || err != nil {
		t.Errorf("unknown slug: ok %v, err %v; want not found, no error", ok, err)
	}
	before := len(queries())
	if _, _, _, err := musora.ResolveInstructorID("jared-falk", "rockstar"); !errors.Is(err, musora.ErrBadBrand) {
		t.Errorf("err = %v, want ErrBadBrand", err)
	}
	if n := len(queries()) - before; n != 0 {
		t.Errorf("Musora was asked %d times for a bad brand, want none", n)
	}
}

// TestIsCoachLink proves a coach page is recognised however it's pasted, so
// a node follow never takes its number for a content id, and a lesson or
// course link isn't.
func TestIsCoachLink(t *testing.T) {
	for _, s := range []string{
		"https://app.musora.com/drumeo/coaches/jared-falk/31880",
		"https://www.musora.com/singeo/coaches/jared-falk/314120?tab=lessons",
		"http://app.musora.com/Drumeo/Coaches/jared-falk",
		"app.musora.com/drumeo/coaches/jared-falk/31880",
		"/pianote/coaches/lisa-witt/",
		"https://www.drumeo.com/laravel/public/drumeo/coaches/jared-falk",
	} {
		if !musora.IsCoachLink(s) {
			t.Errorf("IsCoachLink(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"409875",
		"https://app.musora.com/drumeo/lessons/course/409875/409875",
		"https://app.musora.com/drumeo/courses/coaches-corner/409875",
		"https://app.musora.com/coaches/jared-falk/31880",
		"https://app.musora.com/rockstar/coaches/jared-falk/31880",
		"https://app.musora.com/drumeo/lessons/1?next=/drumeo/coaches/x/2",
	} {
		if musora.IsCoachLink(s) {
			t.Errorf("IsCoachLink(%q) = true, want false", s)
		}
	}
}
