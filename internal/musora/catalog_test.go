package musora

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// serveSanity points the GROQ endpoint at a test server returning body (200) for
// any request, restoring the real base when the test ends.
func serveSanity(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := sanityBase
	sanityBase = srv.URL
	t.Cleanup(func() { sanityBase = orig })
}

// TestHierarchyPropagatesDecodeError: a malformed `result` (valid outer JSON but
// not the expected array) must surface as an error so the planner treats it as a
// retryable expansion failure rather than a silent "zero lessons".
func TestHierarchyPropagatesDecodeError(t *testing.T) {
	serveSanity(t, `{"result": "not-an-array"}`)
	if _, err := Hierarchy(409875, ""); err == nil {
		t.Fatal("Hierarchy: want a decode error on a malformed result, got nil")
	}
}

// TestTopParentPropagatesDecodeError: same contract for the top-parent lookup.
func TestTopParentPropagatesDecodeError(t *testing.T) {
	serveSanity(t, `{"result": "not-an-array"}`)
	if _, err := TopParent(409918, ""); err == nil {
		t.Fatal("TopParent: want a decode error on a malformed result, got nil")
	}
}

// TestHierarchyEmptyResultIsNotAnError: an empty (well-formed) result is the
// legitimate "no hierarchy" case and must return (nil, nil), not an error.
func TestHierarchyEmptyResultIsNotAnError(t *testing.T) {
	serveSanity(t, `{"result": []}`)
	node, err := Hierarchy(409875, "")
	if err != nil {
		t.Fatalf("Hierarchy on empty result: unexpected error %v", err)
	}
	if node != nil {
		t.Errorf("Hierarchy on empty result = %+v, want nil", node)
	}
}

func ids(ns []Node) []int {
	out := []int{}
	for _, n := range ns {
		out = append(out, n.RailcontentID)
	}
	return out
}

func TestCollectLeaves(t *testing.T) {
	if got := ids(CollectLeaves(&Node{RailcontentID: 7}, nil)); !reflect.DeepEqual(got, []int{7}) {
		t.Fatalf("single leaf = %v", got)
	}
	tree := &Node{RailcontentID: 1, Children: []Node{
		{RailcontentID: 2, Children: []Node{{RailcontentID: 4}, {RailcontentID: 5}}},
		{RailcontentID: 3},
	}}
	if got := ids(CollectLeaves(tree, nil)); !reflect.DeepEqual(got, []int{4, 5, 3}) {
		t.Fatalf("nested = %v, want [4 5 3]", got)
	}
}

func TestLeafIDsDropsZeroID(t *testing.T) {
	// A tree whose leaves include a zero-id (structural) leaf: that leaf must be
	// dropped, the real railcontent leaves kept in order.
	tree := &Node{RailcontentID: 1, Children: []Node{
		{RailcontentID: 4},
		{RailcontentID: 0}, // structural leaf with no railcontent reference
		{RailcontentID: 5},
	}}
	got := leafIDs(CollectLeaves(tree, nil))
	if !reflect.DeepEqual(got, []int{4, 5}) {
		t.Fatalf("leafIDs = %v, want [4 5] (zero-id leaf dropped)", got)
	}
}
