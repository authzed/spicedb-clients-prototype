package schema

import "sort"

// SubjectPosition is a subject's identity independent of ID, caveat and
// expiration: "type" for an object (or a wildcard of that type) and
// "type#relation" for a subject set.
func SubjectPosition(definition, relation string) string {
	if relation == "" {
		return definition
	}
	return definition + "#" + relation
}

// ReachingSubjectPositions returns, for every relation and permission of every
// definition (keyed "definition#name"), the sorted subject positions whose
// subjects can reach it. Subject sets are followed transitively: a relation
// admitting team#member is reached both by team#member and by every position
// that reaches team#member. A wildcard contributes its type, since it grants
// every object of that type. Generators use it to answer by-name lookups for a
// subject type that cannot reach the position without calling the server.
func (s *Schema) ReachingSubjectPositions() map[string][]string {
	direct := map[string][]SubjectType{}
	for _, def := range s.Definitions {
		for _, r := range def.Relations {
			direct[SubjectPosition(def.Name, r.Name)] = r.AllowedSubjects
		}
		for _, p := range def.Permissions {
			direct[SubjectPosition(def.Name, p.Name)] = p.ReachableSubjects
		}
	}
	out := make(map[string][]string, len(direct))
	for position := range direct {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(p string) {
			for _, st := range direct[p] {
				key := st.Definition
				if !st.Wildcard {
					key = SubjectPosition(st.Definition, st.Relation)
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				if st.Relation != "" && !st.Wildcard {
					walk(key)
				}
			}
		}
		walk(position)
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out[position] = keys
	}
	return out
}

// SubjectSetPositions returns, in schema order, every "definition#relation"
// that some relation admits or some permission reaches as a subject set. Only
// these can be built as subjects by name; a relation never used as a subject
// has no generated subject ref.
func (s *Schema) SubjectSetPositions() []string {
	used := map[string]bool{}
	note := func(subjects []SubjectType) {
		for _, st := range subjects {
			if st.Relation != "" && !st.Wildcard {
				used[SubjectPosition(st.Definition, st.Relation)] = true
			}
		}
	}
	for _, def := range s.Definitions {
		for _, r := range def.Relations {
			note(r.AllowedSubjects)
		}
		for _, p := range def.Permissions {
			note(p.ReachableSubjects)
		}
	}
	var out []string
	for _, def := range s.Definitions {
		for _, r := range def.Relations {
			if used[SubjectPosition(def.Name, r.Name)] {
				out = append(out, SubjectPosition(def.Name, r.Name))
			}
		}
		for _, p := range def.Permissions {
			if used[SubjectPosition(def.Name, p.Name)] {
				out = append(out, SubjectPosition(def.Name, p.Name))
			}
		}
	}
	return out
}
