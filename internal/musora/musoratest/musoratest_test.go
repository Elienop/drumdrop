package musoratest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// recordingTB stands in for the test the fake reports to, so a test here can
// see what the fake fails it for without failing itself. Everything but
// Errorf goes to the real test.
type recordingTB struct {
	testing.TB
	mu   sync.Mutex
	errs []string
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recordingTB) errors() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.errs)
}

// lookup is ResolveInstructorID's query for slug, as musora sends it.
func lookup(slug string) string {
	return "*[_type=='instructor' && slug.current=='" + slug + "']{ '_id': _id, name, brand }"
}

// lessons is an instructor-lessons filter for slug's documents, narrowed to
// docBrand's when it isn't empty and cut to the first when first, then to
// lessons of brand.
func lessons(slug, docBrand string, first bool, brand string) string {
	sub := "*[_type=='instructor' && slug.current=='" + slug + "'"
	if docBrand != "" {
		sub += " && brand=='" + docBrand + "'"
	}
	sub += "]"
	if first {
		sub += "[0]"
	}
	return "*[references(" + sub + "._id) && brand=='" + brand + "' && status=='published']{ 'id': railcontent_id, 'type': _type, title }"
}

// TestServeAnswersMusorasOwnQueries drives the fake through musora's own
// client: it answers ResolveInstructorID and InstructorLessons from the
// catalog, and lists what it was sent without the queries' comment lines.
func TestServeAnswersMusorasOwnQueries(t *testing.T) {
	queries := Serve(t, JaredFalk())

	id, name, ok, err := musora.ResolveInstructorID("jared-falk", "drumeo")
	if err != nil || !ok || id != "instructor_jaredfalk_31880" || name != "Jared Falk" {
		t.Errorf("ResolveInstructorID = %q %q ok %v err %v; want the drumeo document", id, name, ok, err)
	}
	refs, err := musora.InstructorLessons("jared-falk", "singeo", "")
	if err != nil {
		t.Fatalf("InstructorLessons: %v", err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, fmt.Sprintf("%d %s %s", r.ID, r.Type, r.Title))
	}
	slices.Sort(got)
	if want := []string{"2001 course Singing Course", "2002 question-and-answer Q&A"}; !slices.Equal(got, want) {
		t.Errorf("singeo lessons = %q, want %q", got, want)
	}

	sent := queries()
	if len(sent) != 2 || sent[0] != lookup("jared-falk") {
		t.Fatalf("queries = %q, want the lookup then the lessons query", sent)
	}
	if strings.Contains(sent[1], "//") || !strings.HasPrefix(sent[1], "*[references(") {
		t.Errorf("lessons query = %q, want it without its comment lines", sent[1])
	}
}

// TestServeReadsALongQueryFromThePostBody proves the fake answers a query
// musora sends as a POST, which it does past 1500 characters.
func TestServeReadsALongQueryFromThePostBody(t *testing.T) {
	queries := Serve(t, JaredFalk())
	slug := strings.Repeat("a", 1600)
	raw, err := musora.Query(lookup(slug), "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if string(raw) != "[]" {
		t.Errorf("result = %s, want []", raw)
	}
	if sent := queries(); len(sent) != 1 || sent[0] != lookup(slug) {
		t.Errorf("queries = %d of them, want the one lookup", len(sent))
	}
}

// TestServeFailsTheTestOnAnUnknownQuery proves a query of a shape the fake
// doesn't know fails the test that sent it, and is answered with a 500.
func TestServeFailsTheTestOnAnUnknownQuery(t *testing.T) {
	rec := &recordingTB{TB: t}
	queries := Serve(rec, JaredFalk())
	const q = "*[_type=='song']{title}"
	if _, err := musora.Query(q, ""); err == nil || !strings.HasPrefix(err.Error(), "sanity 500: unknown query") {
		t.Errorf("Query of an unknown shape: %v, want musora's error for a 500 \"unknown query\"", err)
	}
	errs := rec.errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "a query of a shape the fake doesn't know: "+q) {
		t.Errorf("the test was failed with %q, want one unknown-shape error naming the query", errs)
	}
	if sent := queries(); !slices.Equal(sent, []string{q}) {
		t.Errorf("queries = %q, want the unknown one recorded", sent)
	}
}

// TestFakeFailsTheTestOnAnUnreadableRequest proves a request the fake can't
// read a query from fails the test, is answered 400 and isn't recorded.
func TestFakeFailsTheTestOnAnUnreadableRequest(t *testing.T) {
	rec := &recordingTB{TB: t}
	f := &fake{t: rec, c: JaredFalk()}
	w := httptest.NewRecorder()
	f.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json")))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if errs := rec.errors(); len(errs) != 1 || !strings.HasPrefix(errs[0], "musoratest: unreadable request: ") {
		t.Errorf("the test was failed with %q, want one unreadable-request error", errs)
	}
	if sent := f.queries(); len(sent) != 0 {
		t.Errorf("queries = %q, want none", sent)
	}
}

// TestServeRestoresTheQueryEndpoint proves a Serve inside a subtest points
// musora at its own fake only until that subtest ends.
func TestServeRestoresTheQueryEndpoint(t *testing.T) {
	Serve(t, JaredFalk())
	t.Run("inner", func(t *testing.T) {
		Serve(t, Catalog{})
		if _, _, ok, err := musora.ResolveInstructorID("jared-falk", "drumeo"); ok || err != nil {
			t.Errorf("inside: ok %v err %v; want the inner, empty catalog's not found", ok, err)
		}
	})
	if _, _, ok, err := musora.ResolveInstructorID("jared-falk", "drumeo"); !ok || err != nil {
		t.Errorf("after: ok %v err %v; want the outer catalog's instructor", ok, err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// TestQueryOf pins how the fake reads a query: from a GET's query parameter
// or a POST's JSON body, without its comment lines, and an error for a POST
// body it can't read or decode.
func TestQueryOf(t *testing.T) {
	const want = "*[_type=='instructor']\n{ name }"
	sent := "// a comment\n  // an indented one\n" + want + "\n"

	get := httptest.NewRequest(http.MethodGet, "/?perspective=published&query="+url.QueryEscape(sent), nil)
	if q, err := queryOf(get); err != nil || q != want {
		t.Errorf("GET: %q, %v; want %q", q, err, want)
	}
	body, _ := json.Marshal(map[string]string{"query": sent})
	post := httptest.NewRequest(http.MethodPost, "/?perspective=published", strings.NewReader(string(body)))
	if q, err := queryOf(post); err != nil || q != want {
		t.Errorf("POST: %q, %v; want %q", q, err, want)
	}
	// The body is valid JSON up to the failure, so only the read error can
	// refuse it.
	cut := io.MultiReader(strings.NewReader(`{"query":"*[_type=='instructor']"}`), failingReader{})
	if _, err := queryOf(httptest.NewRequest(http.MethodPost, "/", cut)); err == nil {
		t.Error("POST with a body that fails partway: no error")
	}
	if _, err := queryOf(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{"))); err == nil {
		t.Error("POST with a body that isn't JSON: no error")
	}
}

// TestAnswer pins what the fake answers for each query shape it knows, the
// way Sanity would: the lookup's documents in _id order, and the lessons of
// a brand that reference any document the subquery selects.
func TestAnswer(t *testing.T) {
	for _, c := range []struct {
		name, query string
		want        string // the result as JSON
	}{
		{"lookup, in _id order", lookup("jared-falk"),
			`[{"_id":"instructor_jaredfalk_314120","brand":"singeo","name":"Jared Falk (Singeo)"},` +
				`{"_id":"instructor_jaredfalk_31880","brand":"drumeo","name":"Jared Falk"}]`},
		{"lookup of an unknown slug", lookup("nobody"), `[]`},
		{"every document's lessons", lessons("jared-falk", "", false, "drumeo"),
			`[{"id":1001,"title":"Drum Course","type":"course"},{"id":1002,"title":"Drum Song","type":"song"}]`},
		{"the drumeo document's singeo lessons", lessons("jared-falk", "drumeo", false, "singeo"),
			`[{"id":2002,"title":"Q&A","type":"question-and-answer"}]`},
		{"the first document's drumeo lessons", lessons("jared-falk", "", true, "drumeo"), `[]`},
		{"the drumeo document, first, drumeo lessons", lessons("jared-falk", "drumeo", true, "drumeo"),
			`[{"id":1001,"title":"Drum Course","type":"course"},{"id":1002,"title":"Drum Song","type":"song"}]`},
		{"an unknown slug's lessons", lessons("nobody", "", false, "drumeo"), `[]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, ok := JaredFalk().answer(c.query)
			if !ok {
				t.Fatalf("answer(%q): not a known shape", c.query)
			}
			var buf strings.Builder
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false) // "Q&A", not "Q&A"
			if err := enc.Encode(result); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSuffix(buf.String(), "\n"); got != c.want {
				t.Errorf("answer = %s\nwant       %s", got, c.want)
			}
		})
	}
	for _, q := range []string{"*[_type=='song']", lookup("Jared Falk"), ""} {
		if result, ok := JaredFalk().answer(q); ok {
			t.Errorf("answer(%q) = %v, known; want an unknown shape", q, result)
		}
	}
}

// TestJaredFalk pins the measured case the fixture's comment describes: two
// documents under one slug, the singeo one sorting first; drumeo lessons
// that reference only the drumeo one; a singeo lesson that references it.
func TestJaredFalk(t *testing.T) {
	c := JaredFalk()
	docs := c.docs("jared-falk", "")
	if len(docs) != 2 || docs[0].Brand != "singeo" || docs[1].Brand != "drumeo" {
		t.Fatalf("documents in _id order = %+v, want singeo's then drumeo's", docs)
	}
	drumeoDoc := docs[1].ID
	if got := c.docs("jared-falk", "drumeo"); len(got) != 1 || got[0].ID != drumeoDoc {
		t.Errorf("drumeo's documents = %+v, want only %s", got, drumeoDoc)
	}
	singeoRefsDrumeo := false
	for _, l := range c.Lessons {
		if l.Brand == "drumeo" && !slices.Equal(l.Refs, []string{drumeoDoc}) {
			t.Errorf("drumeo lesson %d references %v, want only %s", l.ID, l.Refs, drumeoDoc)
		}
		if l.Brand == "singeo" && slices.Contains(l.Refs, drumeoDoc) {
			singeoRefsDrumeo = true
		}
	}
	if !singeoRefsDrumeo {
		t.Error("no singeo lesson references the drumeo document")
	}
}
