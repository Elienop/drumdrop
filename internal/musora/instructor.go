package musora

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// LessonRef is a lightweight reference to a downloadable lesson returned by the
// instructor-lessons query: just enough to drive resolve + download.
type LessonRef struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// reSlug is the slug allowlist pattern. Slugs come from our own catalog data, not
// arbitrary user input, but because the slug is substituted into a GROQ string
// literal we validate it (defense-in-depth against GROQ injection) before use.
var reSlug = regexp.MustCompile(`^[a-z0-9-]+$`)

// allowedBrands is the set of Musora brands we may substitute into the query.
var allowedBrands = map[string]bool{
	"drumeo":   true,
	"pianote":  true,
	"guitareo": true,
	"singeo":   true,
	"playbass": true,
}

// ErrBadSlug is what an error wraps when an instructor slug is not of the form
// Musora uses (reSlug), so it was refused before any network call.
var ErrBadSlug = errors.New("invalid instructor slug")

// ErrBadBrand is what an error wraps when a brand is not one of Musora's
// (allowedBrands), so it was refused before any network call.
var ErrBadBrand = errors.New("invalid brand")

func validateSlug(slug string) error {
	if !reSlug.MatchString(slug) {
		return fmt.Errorf("%w %q: must match ^[a-z0-9-]+$", ErrBadSlug, slug)
	}
	return nil
}

// ValidateBrand returns an error wrapping ErrBadBrand when brand is not one of
// Musora's brands, for a caller that stores a brand before any query uses it.
func ValidateBrand(brand string) error {
	if !allowedBrands[brand] {
		return fmt.Errorf("%w %q: not in allowlist", ErrBadBrand, brand)
	}
	return nil
}

// buildInstructorQuery validates slug/brand and substitutes the unique sentinels
// in the embedded template. It returns the ready-to-run GROQ (the [92] permission
// list is still rewritten by ApplyPermissions inside Query).
func buildInstructorQuery(slug, brand string) (string, error) {
	if err := validateSlug(slug); err != nil {
		return "", err
	}
	if err := ValidateBrand(brand); err != nil {
		return "", err
	}
	tpl, err := LoadQuery("instructor_lessons")
	if err != nil {
		return "", err
	}
	tpl = strings.Replace(tpl, "$SLUG$", slug, 1)
	tpl = strings.Replace(tpl, "$BRAND$", brand, 1)
	return tpl, nil
}

// parseLessonRefs unmarshals a Sanity `result` array into []LessonRef and drops
// any row with a zero/null id (instructor docs and other containers have a null
// railcontent_id and must not be treated as downloadable lessons).
func parseLessonRefs(raw []byte) ([]LessonRef, error) {
	var rows []LessonRef
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]LessonRef, 0, len(rows))
	for _, r := range rows {
		if r.ID == 0 {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// InstructorLessons returns the downloadable lessons that reference the instructor
// identified by slug, scoped to brand. slug must match ^[a-z0-9-]+$ and brand must
// be in the allowlist; both are validated before substitution to keep the GROQ
// well-formed (defense-in-depth against query injection).
func InstructorLessons(slug, brand, permIDs string) ([]LessonRef, error) {
	tpl, err := buildInstructorQuery(slug, brand)
	if err != nil {
		return nil, err
	}
	raw, err := Query(tpl, permIDs)
	if err != nil {
		return nil, err
	}
	return parseLessonRefs(raw)
}

// ResolveInstructorID verifies the instructor exists and returns its Sanity _id
// and display name. ok is false (with nil error) when no instructor matches the
// slug. Used by `follow @slug` to populate the follow title. slug is validated
// the same way as InstructorLessons.
func ResolveInstructorID(slug string) (id, name string, ok bool, err error) {
	if err = validateSlug(slug); err != nil {
		return "", "", false, err
	}
	q := "*[_type=='instructor' && slug.current=='" + slug + "']{ '_id': _id, name }"
	raw, err := Query(q, "")
	if err != nil {
		return "", "", false, err
	}
	var rows []struct {
		ID   string `json:"_id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return "", "", false, err
	}
	if len(rows) == 0 || rows[0].ID == "" {
		return "", "", false, nil
	}
	return rows[0].ID, rows[0].Name, true, nil
}
