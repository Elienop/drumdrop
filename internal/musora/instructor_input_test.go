package musora

import (
	"errors"
	"testing"
)

// TestNormalizeInstructorAccepts pins every form ruling #70 accepts: a slug, a
// name in any case with its spaces turned into hyphens, surrounding
// whitespace, and a coach-page link in the shape of Musora's web_url_path,
// whose brand fills an empty brand.
func TestNormalizeInstructorAccepts(t *testing.T) {
	for _, c := range []struct {
		input, brand    string
		slug, wantBrand string
	}{
		{"jared-falk", "", "jared-falk", "drumeo"},
		{"Jared Falk", "", "jared-falk", "drumeo"},
		{"Jared-Falk", "", "jared-falk", "drumeo"},
		{"JARED FALK", "pianote", "jared-falk", "pianote"},
		{"jared   falk", "", "jared-falk", "drumeo"},
		{"  Jared Falk \n", "", "jared-falk", "drumeo"},
		{"mike2", "", "mike2", "drumeo"},
		{"https://app.musora.com/drumeo/coaches/jared-falk/31880", "", "jared-falk", "drumeo"},
		{"https://www.musora.com/singeo/coaches/jared-falk/314120", "", "jared-falk", "singeo"},
		{"https://www.musora.com/singeo/coaches/jared-falk/314120", "singeo", "jared-falk", "singeo"},
		{"https://app.musora.com/pianote/coaches/lisa-witt", "", "lisa-witt", "pianote"},
		{"https://app.musora.com/drumeo/coaches/jared-falk/31880/", "", "jared-falk", "drumeo"},
		{"HTTPS://app.musora.com/drumeo/coaches/jared-falk/31880?tab=lessons#top", "", "jared-falk", "drumeo"},
		{"http://app.musora.com/drumeo/coaches/jared-falk", "", "jared-falk", "drumeo"},
		{" https://app.musora.com/drumeo/coaches/jared-falk/31880\n", "", "jared-falk", "drumeo"},
	} {
		slug, brand, err := NormalizeInstructor(c.input, c.brand)
		if err != nil || slug != c.slug || brand != c.wantBrand {
			t.Errorf("NormalizeInstructor(%q, %q) = %q, %q, %v; want %q, %q, nil",
				c.input, c.brand, slug, brand, err, c.slug, c.wantBrand)
		}
	}
}

// TestNormalizeInstructorRefuses pins what is refused rather than guessed at:
// what Musora's slug form can't hold, characters whose slug spelling isn't
// known (accents, look-alikes, underscores, apostrophes, dots), whitespace
// other than a space inside the input, and links that aren't a coach page.
func TestNormalizeInstructorRefuses(t *testing.T) {
	for _, input := range []string{
		"",
		"   ",
		"\n",
		`say "hi"`,
		`x'||true||'`,
		"o'brien",
		"a\nb",
		"a\r\nb",
		"a\tb",
		"jared_falk",
		"jared.falk",
		"José Pérez",
		"jared falk",             // no-break space
		"Kenny",                  // Kelvin sign, which strings.ToLower folds into k
		"jared-falk]{_id}[0",     // GROQ brackets
		"slug'][0]._id)||true//", // GROQ injection
		"app.musora.com/drumeo/coaches/jared-falk/31880", // no scheme
		"https://app.musora.com/drumeo/lessons/course/409875/409875",
		"https://app.musora.com/drumeo/coaches",
		"https://app.musora.com/drumeo/coaches/",
		"https://app.musora.com/drumeo/coaches/jared-falk/31880/lessons",
		"https://app.musora.com/drumeo/coaches/jared-falk/abc",
		"https://app.musora.com/rockstar/coaches/jared-falk/31880",
		"https://app.musora.com/Drumeo/coaches/jared-falk/31880",
		"https://app.musora.com/drumeo/coaches/Jared-Falk/31880",
		"https://app.musora.com/drumeo/coaches/jared%20falk/31880",
		"https://app.musora.com/drumeo/coaches/jared%27falk/31880",
		"https://app.musora.com/x/drumeo/coaches/jared-falk/31880",
		"https://app.musora.com//drumeo/coaches/jared-falk",
		"https://www.drumeo.com/laravel/public/drumeo/coaches/jared-falk",
		"https:///drumeo/coaches/jared-falk",
		"https://app.musora.com/drumeo/coaches/jared-falk\nx",
	} {
		if slug, brand, err := NormalizeInstructor(input, ""); !errors.Is(err, ErrBadSlug) {
			t.Errorf("NormalizeInstructor(%q) = %q, %q, %v; want ErrBadSlug", input, slug, brand, err)
		}
	}
}

// TestNormalizeInstructorBrand proves the brand given is checked before the
// input is read, and that a link's brand never silently overrides one the
// user gave: a different one is ErrBrandMismatch.
func TestNormalizeInstructorBrand(t *testing.T) {
	if _, _, err := NormalizeInstructor("jared-falk", "Drumeo"); !errors.Is(err, ErrBadBrand) {
		t.Errorf("brand Drumeo: err = %v, want ErrBadBrand", err)
	}
	if _, _, err := NormalizeInstructor("not a slug!", "rockstar"); !errors.Is(err, ErrBadBrand) {
		t.Errorf("bad brand and bad input: err = %v, want ErrBadBrand first", err)
	}
	link := "https://app.musora.com/singeo/coaches/jared-falk/314120"
	if _, _, err := NormalizeInstructor(link, "drumeo"); !errors.Is(err, ErrBrandMismatch) {
		t.Errorf("singeo link, brand drumeo: err = %v, want ErrBrandMismatch", err)
	}
}
