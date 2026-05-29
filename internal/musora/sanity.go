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

const sanityBase = "https://sanity.musora.com/4032r8py/apicdn/v2021-06-07/production_v2/v4"
const browserUA = "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0"
const getMax = 1500

var (
	reFirstID  = regexp.MustCompile(`railcontent_id\s*==\s*\d+`)
	rePermIDs  = regexp.MustCompile(`(array::intersects\([^,]+,\s*)\[[\d,\s]*\]`)
	httpClient = &http.Client{}
)

func LoadQuery(name string) (string, error) {
	b, err := queryFS.ReadFile("queries/" + name + ".groq")
	return string(b), err
}

// WithID replaces only the first `railcontent_id == <n>` filter.
func WithID(template string, id int) string {
	return reFirstID.ReplaceAllString(template, fmt.Sprintf("railcontent_id == %d", id))
}

// ApplyPermissions rewrites array::intersects(...,[..]) to the configured ids (default "92").
func ApplyPermissions(query, permissionIDs string) string {
	if permissionIDs == "" {
		permissionIDs = "92"
	}
	return rePermIDs.ReplaceAllString(query, "${1}["+permissionIDs+"]")
}

// Query runs a GROQ query (GET when short, POST when long) and returns the raw `result`.
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
