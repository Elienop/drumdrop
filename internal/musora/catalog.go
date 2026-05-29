package musora

import (
	"encoding/json"
	"fmt"
)

type Node struct {
	RailcontentID int    `json:"railcontent_id"`
	Children      []Node `json:"children"`
}

func TopParent(id int, permIDs string) (int, error) {
	tpl, err := LoadQuery("top_parent")
	if err != nil {
		return 0, err
	}
	raw, err := Query(WithID(tpl, id), permIDs)
	if err != nil {
		return 0, err
	}
	var res []struct {
		TopParent int `json:"top_parent"`
	}
	// A malformed response must surface as a (wrapped) error so the planner
	// retries the expansion instead of treating it as "no parent". An empty
	// decoded slice is a legitimate "no parent" result -> fall back to id.
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("top_parent: decode response: %w", err)
	}
	if len(res) == 0 {
		return id, nil
	}
	return res[0].TopParent, nil
}

func Hierarchy(rootID int, permIDs string) (*Node, error) {
	tpl, err := LoadQuery("hierarchy_children")
	if err != nil {
		return nil, err
	}
	raw, err := Query(WithID(tpl, rootID), permIDs)
	if err != nil {
		return nil, err
	}
	var res []Node
	// A malformed response must surface as a (wrapped) error so the planner
	// retries the expansion instead of treating it as "zero lessons". An empty
	// decoded slice is a legitimate "no hierarchy" result -> return nil, nil.
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("hierarchy_children: decode response: %w", err)
	}
	if len(res) == 0 {
		return nil, nil
	}
	return &res[0], nil
}

// CollectLeaves returns leaf nodes depth-first; if root has no children it returns [root].
func CollectLeaves(n *Node, acc []Node) []Node {
	if n == nil {
		return acc
	}
	if len(n.Children) == 0 {
		return append(acc, *n)
	}
	for i := range n.Children {
		acc = CollectLeaves(&n.Children[i], acc)
	}
	return acc
}

// leafIDs returns the RailcontentIDs of the given leaves, dropping any leaf with
// a zero id (e.g. a structural node with no railcontent reference).
func leafIDs(leaves []Node) []int {
	var ids []int
	for _, leaf := range leaves {
		if leaf.RailcontentID != 0 {
			ids = append(ids, leaf.RailcontentID)
		}
	}
	return ids
}

// ResolveLessonIDs: whole=false -> exactly the node pointed at; whole=true -> walk to top parent.
func ResolveLessonIDs(targetID int, whole bool, permIDs string) (rootID int, lessonIDs []int, err error) {
	rootID = targetID
	if whole {
		if rootID, err = TopParent(targetID, permIDs); err != nil {
			return
		}
	}
	root, err := Hierarchy(rootID, permIDs)
	if err != nil {
		return
	}
	lessonIDs = leafIDs(CollectLeaves(root, nil))
	return
}
