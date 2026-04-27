package values

// MergeValues deep-merges overlay onto base. Maps merge by key (overlay wins
// on conflict, recursively); any non-map overlay replaces base entirely.
func MergeValues(base, overlay *Node) *Node {
	if base == nil {
		return overlay
	}
	if overlay == nil {
		return base
	}
	if base.Type != TypeMap || overlay.Type != TypeMap {
		return overlay
	}
	merged := map[string]*Node{}
	for k, v := range base.Children {
		merged[k] = v
	}
	for k, v := range overlay.Children {
		if existing, ok := merged[k]; ok {
			merged[k] = MergeValues(existing, v)
		} else {
			merged[k] = v
		}
	}
	desc := overlay.Description
	if desc == "" {
		desc = base.Description
	}
	example := overlay.Example
	if example == "" {
		example = base.Example
	}
	return &Node{
		Type:        TypeMap,
		Description: desc,
		Example:     example,
		Children:    merged,
	}
}

// MergeAll layers nodes left-to-right; later entries win.
func MergeAll(nodes []*Node) *Node {
	if len(nodes) == 0 {
		return nil
	}
	cur := nodes[0]
	for i := 1; i < len(nodes); i++ {
		cur = MergeValues(cur, nodes[i])
	}
	return cur
}
