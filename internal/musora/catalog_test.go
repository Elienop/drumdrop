package musora

import (
	"reflect"
	"testing"
)

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
