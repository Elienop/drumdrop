// Package musoratest serves a fake of Musora's Sanity endpoint that answers
// DrumDrop's two instructor queries from a small catalog, the way Sanity
// would, for tests in any package. It's imported by tests only.
//
// It models what the real catalog was measured to do (2026-09-24): one
// instructor document per brand under one slug, sometimes two in one brand;
// an unordered query answers them in _id order; and a brand's lessons may
// reference any of them. A query of any other shape fails the test, so a
// change to either query must teach the fake its meaning first.
package musoratest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/elienop/drumdrop/internal/musora"
)

// Instructor is one instructor document.
type Instructor struct {
	ID, Slug, Brand, Name string
}

// Lesson is one published, playable lesson of Brand that references the
// instructor documents whose _ids are in Refs.
type Lesson struct {
	ID          int
	Type, Title string
	Brand       string
	Refs        []string
}

// Catalog is what the fake answers from.
type Catalog struct {
	Instructors []Instructor
	Lessons     []Lesson
}

var (
	// reLookup is ResolveInstructorID's query.
	reLookup = regexp.MustCompile(`^\*\[_type=='instructor' && slug\.current=='([a-z0-9-]+)'\]\{[^}]*\}$`)
	// reLessons is instructor_lessons.groq's filter: the instructor subquery,
	// optionally narrowed by brand and cut to its first document, then the
	// lesson's brand.
	reLessons = regexp.MustCompile(`\*\[references\(\*\[_type=='instructor' && slug\.current=='([a-z0-9-]+)'( && brand=='([a-z]+)')?\](\[0\])?\._id\) && brand=='([a-z]+)' && `)
)

// Serve starts the fake, points musora's query endpoint at it until the test
// ends, and returns a func that lists the queries it was sent.
func Serve(t testing.TB, c Catalog) func() []string {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, err := queryOf(r)
		if err != nil {
			t.Errorf("musoratest: unreadable request: %v", err)
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		result, ok := c.answer(q)
		if !ok {
			t.Errorf("musoratest: a query of a shape the fake doesn't know: %s", q)
			http.Error(w, "unknown query", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(musora.SetSanityBase(srv.URL))
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(queries)
	}
}

// queryOf reads the GROQ from a GET's query parameter or a POST's JSON body,
// the two ways musora.Query sends it, without its leading comment lines.
func queryOf(r *http.Request) (string, error) {
	q := r.URL.Query().Get("query")
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return "", err
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			return "", err
		}
		q = body.Query
	}
	var lines []string
	for _, l := range strings.Split(q, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "//") {
			lines = append(lines, l)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

// answer evaluates q against the catalog; ok is false for a shape it
// doesn't know.
func (c Catalog) answer(q string) (any, bool) {
	if m := reLookup.FindStringSubmatch(q); m != nil {
		rows := []map[string]string{}
		for _, d := range c.docs(m[1], "") {
			rows = append(rows, map[string]string{"_id": d.ID, "name": d.Name, "brand": d.Brand})
		}
		return rows, true
	}
	m := reLessons.FindStringSubmatch(q)
	if m == nil {
		return nil, false
	}
	docs := c.docs(m[1], m[3])
	if m[4] != "" && len(docs) > 1 {
		docs = docs[:1]
	}
	ids := map[string]bool{}
	for _, d := range docs {
		ids[d.ID] = true
	}
	rows := []map[string]any{}
	for _, l := range c.Lessons {
		if l.Brand == m[5] && slices.ContainsFunc(l.Refs, func(r string) bool { return ids[r] }) {
			rows = append(rows, map[string]any{"id": l.ID, "type": l.Type, "title": l.Title})
		}
	}
	return rows, true
}

// docs returns the instructor documents filed under slug, in brand when
// brand isn't empty, in _id order as Sanity answers an unordered query.
func (c Catalog) docs(slug, brand string) []Instructor {
	var out []Instructor
	for _, d := range c.Instructors {
		if d.Slug == slug && (brand == "" || d.Brand == brand) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b Instructor) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// JaredFalk is the measured case: his singeo document sorts before his drumeo
// one, and the drumeo lessons reference only the drumeo one, so a query that
// takes the first document finds no drumeo lessons at all. The singeo
// document's name is changed here so a test can tell which one was picked
// (live, both say "Jared Falk"). His singeo lessons reference either.
func JaredFalk() Catalog {
	return Catalog{
		Instructors: []Instructor{
			{ID: "instructor_jaredfalk_31880", Slug: "jared-falk", Brand: "drumeo", Name: "Jared Falk"},
			{ID: "instructor_jaredfalk_314120", Slug: "jared-falk", Brand: "singeo", Name: "Jared Falk (Singeo)"},
		},
		Lessons: []Lesson{
			{ID: 1001, Type: "course", Title: "Drum Course", Brand: "drumeo", Refs: []string{"instructor_jaredfalk_31880"}},
			{ID: 1002, Type: "song", Title: "Drum Song", Brand: "drumeo", Refs: []string{"instructor_jaredfalk_31880"}},
			{ID: 2001, Type: "course", Title: "Singing Course", Brand: "singeo", Refs: []string{"instructor_jaredfalk_314120"}},
			{ID: 2002, Type: "question-and-answer", Title: "Q&A", Brand: "singeo", Refs: []string{"instructor_jaredfalk_31880"}},
		},
	}
}
