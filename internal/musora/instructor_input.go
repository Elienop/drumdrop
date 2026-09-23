package musora

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// DefaultBrand is the brand a follow gets when neither the user nor a pasted
// link names one.
const DefaultBrand = "drumeo"

// ErrBrandMismatch is what NormalizeInstructor answers when a pasted coach-page
// link names one brand and the brand given alongside it names another.
var ErrBrandMismatch = errors.New("the link's brand differs from the brand given")

// coachesSegment is the path segment that marks a coach page. Musora's
// instructor documents carry their page as web_url_path, in the form
// "/<brand>/coaches/<slug>/<id>" (read from the live instructor documents for
// jared-falk: "/drumeo/coaches/jared-falk/31880" and
// "/singeo/coaches/jared-falk/314120").
const coachesSegment = "coaches"

// NormalizeInstructor turns what a user typed to name an instructor into the
// slug Musora files them under, and settles the brand to follow them in. It is
// the one normalisation the preview, the add-follow route and the CLI share,
// so what a preview shows is what an add stores.
//
// input is accepted in three forms:
//   - a slug, "jared-falk";
//   - a name, "Jared Falk": ASCII only, lower-cased, each run of spaces
//     turned into one hyphen;
//   - a coach-page link, "https://<host>/drumeo/coaches/jared-falk/31880",
//     whose slug is taken as it stands and whose brand is used when brand is
//     empty. The host isn't checked: web_url_path gives only the path.
//
// Surrounding whitespace is trimmed. Anything else is refused rather than
// guessed at: non-ASCII (accents, look-alike letters), underscores,
// apostrophes, dots, tabs and newlines inside the input, and any link that
// isn't a coach page. A refusal wraps ErrBadSlug, and none reaches the network.
//
// brand is the one the user gave, or "" for none. It must be one of Musora's
// (ErrBadBrand), and when a link names another it's ErrBrandMismatch. With
// neither, the brand is DefaultBrand.
func NormalizeInstructor(input, brand string) (slug, outBrand string, err error) {
	if brand != "" {
		if err := ValidateBrand(brand); err != nil {
			return "", "", err
		}
	}
	s := strings.TrimSpace(input)
	linkBrand := ""
	if isLink(s) {
		slug, linkBrand, err = slugFromCoachLink(s)
	} else {
		slug, err = slugFromName(s)
	}
	if err != nil {
		return "", "", err
	}
	switch {
	case linkBrand == "" && brand == "":
		brand = DefaultBrand
	case brand == "":
		brand = linkBrand
	case linkBrand != "" && linkBrand != brand:
		return "", "", fmt.Errorf("%w: link %q, given %q", ErrBrandMismatch, linkBrand, brand)
	}
	return slug, brand, nil
}

// isLink reports whether s is meant as a web link: it starts with an http or
// https scheme, in any case.
func isLink(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// slugFromName lower-cases a name or slug and turns each run of spaces into
// one hyphen, then checks the result has Musora's slug form. Only the ASCII
// space is joined, so a tab or newline inside a name is refused. It works on
// bytes and folds only A-Z: strings.ToLower would fold a look-alike, such as
// the Kelvin sign, into an ASCII letter that then passes; here every
// non-ASCII byte is left as it is, for validateSlug to refuse.
func slugFromName(s string) (string, error) {
	var b strings.Builder
	space := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' {
			space = true
			continue
		}
		if space {
			b.WriteByte('-')
			space = false
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	slug := b.String()
	if err := validateSlug(slug); err != nil {
		return "", err
	}
	return slug, nil
}

// slugFromCoachLink takes the slug and brand out of a coach-page link, whose
// path is "/<brand>/coaches/<slug>" with an optional numeric id and trailing
// slash; the query and fragment are ignored. s is an http or https link
// (isLink), and with a host its path is empty or starts with "/", so parts[0]
// is always "". The path is read escaped, so a percent-encoded character fails
// the checks rather than being decoded into one that passes.
func slugFromCoachLink(s string) (slug, brand string, err error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("%w: not a link that can be read", ErrBadSlug)
	}
	parts := strings.Split(strings.TrimSuffix(u.EscapedPath(), "/"), "/")
	if len(parts) == 5 && isDigits(parts[4]) {
		parts = parts[:4]
	}
	if len(parts) != 4 || parts[2] != coachesSegment {
		return "", "", fmt.Errorf("%w: not a link to a coach page", ErrBadSlug)
	}
	if !allowedBrands[parts[1]] {
		return "", "", fmt.Errorf("%w: the link's brand isn't one of Musora's", ErrBadSlug)
	}
	if err := validateSlug(parts[3]); err != nil {
		return "", "", err
	}
	return parts[3], parts[1], nil
}

// IsCoachLink reports whether s looks like a link to a coach page: a path
// segment "coaches" right after one of Musora's brands, in any case, with or
// without a scheme and host; the query and fragment are ignored. A node
// follow asks it so a coach page's number is never taken for a content id
// (engine.ExtractID takes a link's last number). It's looser than
// NormalizeInstructor on purpose, because it only ever refuses.
func IsCoachLink(s string) bool {
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(strings.ToLower(s), "/")
	for i := 1; i < len(parts); i++ {
		if parts[i] == coachesSegment && allowedBrands[parts[i-1]] {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
