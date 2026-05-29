package musora

import "encoding/json"

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
	if err := json.Unmarshal(raw, &res); err != nil || len(res) == 0 {
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
	if err := json.Unmarshal(raw, &res); err != nil || len(res) == 0 {
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
	for _, leaf := range CollectLeaves(root, nil) {
		if leaf.RailcontentID != 0 {
			lessonIDs = append(lessonIDs, leaf.RailcontentID)
		}
	}
	return
}
