package midi

import "sort"

// ModSet is declared in mapping.go alongside the Table it keys. Its methods
// live here because they exist to serve resolution.

// With returns a COPY with name held. Copying matters because the resolver
// holds a set across the hot path; mutating one in place would change
// resolution mid-performance.
func (m ModSet) With(name string) ModSet {
	out := make(ModSet, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out[name] = true
	return out
}

// Without returns a COPY with name released.
func (m ModSet) Without(name string) ModSet {
	out := make(ModSet, len(m))
	for k, v := range m {
		out[k] = v
	}
	delete(out, name)
	return out
}

// Empty reports whether no modifiers are held.
func (m ModSet) Empty() bool {
	for _, v := range m {
		if v {
			return false
		}
	}
	return true
}

// Names returns the held modifier names, sorted. Sorting is what makes
// resolution deterministic: map iteration order is randomised in Go, and a
// resolution that depended on it would change behaviour between runs.
func (m ModSet) Names() []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Canonical returns the sorted, comma-joined spelling of the held set, which is
// the table key used by Compile.
func (m ModSet) Canonical() string { return canonicalMods(m.Names()) }

// combinations returns every size-element subset of names, in the order the
// names appear (which are already sorted). Enumerating subsets rather than
// walking a fixed fallback chain is what lets an unrelated held modifier fall
// through to a less specific binding instead of failing to resolve at all.
func combinations(names []string, size int) [][]string {
	if size == 0 {
		return [][]string{nil}
	}
	if size > len(names) {
		return nil
	}
	var out [][]string
	var rec func(start int, cur []string)
	rec = func(start int, cur []string) {
		if len(cur) == size {
			cp := make([]string, size)
			copy(cp, cur)
			out = append(out, cp)
			return
		}
		for i := start; i < len(names); i++ {
			rec(i+1, append(cur, names[i]))
		}
	}
	rec(0, nil)
	return out
}

// Resolve finds the binding for a message given the held modifiers, trying
// modifier sets from most specific to least (spec §5.3).
//
// The order is by descending subset size, then by the sorted order within a
// size, so it never depends on map iteration. Size ordering is the whole point:
// holding ctrl+shift must prefer a [shift, ctrl] binding over a [shift] one,
// while holding ctrl alone must fall through to the unmodified binding because
// no [ctrl] binding exists.
func (t *Table) Resolve(mods ModSet, msg Message) (Control, bool) {
	names := mods.Names()
	k := msgKey{Kind: msg.Kind, Chan: msg.Channel, Num: msg.Num}
	for size := len(names); size >= 0; size-- {
		for _, subset := range combinations(names, size) {
			bucket, ok := t.byMods[canonicalMods(subset)]
			if !ok {
				continue
			}
			if c, ok := bucket[k]; ok {
				return c, true
			}
		}
	}
	return Control{}, false
}

// Lookup is Lookup's simpler sibling: it matches only the exact held set, with
// no subset fallback. It exists for callers that have already decided which
// modifier set applies; Resolve is what the input path uses.
func (t *Table) LookupExact(mods ModSet, msg Message) (Control, bool) {
	bucket, ok := t.byMods[mods.Canonical()]
	if !ok {
		return Control{}, false
	}
	c, ok := bucket[msgKey{Kind: msg.Kind, Chan: msg.Channel, Num: msg.Num}]
	return c, ok
}
