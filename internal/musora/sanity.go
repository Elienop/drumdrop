package musora

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
)

//go:embed queries/*.groq
var queryFS embed.FS

// sanityBase is the GROQ read endpoint. It is a var (not const) so tests can
// point Query at an httptest server; production never reassigns it.
var sanityBase = "https://sanity.musora.com/4032r8py/apicdn/v2021-06-07/production_v2/v4"

// SetSanityBase repoints the GROQ read endpoint at base and returns a func that
// restores the previous value. It exists so tests in other packages (e.g. the
// HTTP server's follow-create handler, which calls ResolveLesson/
// ResolveInstructorID) can run hermetically against an httptest stub. Production
// never calls it.
func SetSanityBase(base string) (restore func()) {
	prev := sanityBase
	sanityBase = base
	return func() { sanityBase = prev }
}

const browserUA = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"
const getMax = 1500

var (
	reFirstID = regexp.MustCompile(`railcontent_id\s*==\s*\d+`)
	rePermIDs = regexp.MustCompile(`(array::intersects\([^,]+,\s*)\[[\d,\s]*\]`)
	// rePermIDValid is a defense-in-depth allowlist for the permission-id list
	// substituted into the GROQ array literal: one or more comma-separated
	// integers (mirrors validateSlug/validateBrand).
	rePermIDValid = regexp.MustCompile(`^[0-9]+(,[0-9]+)*$`)
	httpClient    = &http.Client{}
)

const defaultPermissionIDs = "92"

func LoadQuery(name string) (string, error) {
	b, err := queryFS.ReadFile("queries/" + name + ".groq")
	return string(b), err
}

// WithID replaces only the first `railcontent_id == <n>` filter, leaving any
// later occurrences (e.g. nested projection references) intact. This mirrors
// src/sanity.mjs withId, which uses String.replace without the /g flag.
func WithID(template string, id int) string {
	loc := reFirstID.FindStringIndex(template)
	if loc == nil {
		return template
	}
	return template[:loc[0]] + fmt.Sprintf("railcontent_id == %d", id) + template[loc[1]:]
}

// ApplyPermissions rewrites array::intersects(...,[..]) to the configured ids (default "92").
func ApplyPermissions(query, permissionIDs string) string {
	if permissionIDs == "" {
		permissionIDs = defaultPermissionIDs
	}
	// Defense-in-depth: the id list is substituted verbatim into a GROQ array
	// literal, so reject anything that is not a comma-separated integer list and
	// fall back to the default rather than injecting arbitrary text.
	if !rePermIDValid.MatchString(permissionIDs) {
		permissionIDs = defaultPermissionIDs
	}
	return rePermIDs.ReplaceAllString(query, "${1}["+permissionIDs+"]")
}

// Query runs a GROQ query (GET when short, POST when long) and returns the raw `result`.
//
// The Sanity content endpoints (and the media URLs fetched by fetchToFile) are
// open-read: they require no session cookie or auth header. Downloads are NOT
// session-gated, which is why nothing here threads a login through.
func Query(query, permissionIDs string) (json.RawMessage, error) {
	q := ApplyPermissions(query, permissionIDs)
	var req *http.Request
	var err error
	if len(q) <= getMax {
		u := sanityBase + "?perspective=published&query=" + url.QueryEscape(q)
		req, err = http.NewRequest(http.MethodGet, u, nil)
	} else {
		body, _ := json.Marshal(map[string]string{"query": q})
		req, err = http.NewRequest(http.MethodPost, sanityBase+"?perspective=published", bytes.NewReader(body))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Referer", "https://app.musora.com/")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sanity %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	var wrap struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, err
	}
	return wrap.Result, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
