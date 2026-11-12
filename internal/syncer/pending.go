package syncer

import (
	"github.com/graemer-org/spellbook/internal/config"
	"github.com/graemer-org/spellbook/internal/state"
)

// PendingItem is a local resource that was pushed to a registry.
type PendingItem struct {
	// Abs is the file or directory of the resource.
	Abs string
	// Kind is one of the state kinds, such as KindSkill.
	Kind, Name, Target, Registry string
}

// MarkPending records pushed resources in the state file, with the content
// they have now. From then on the sync treats them as its own: it keeps
// them while the registry does not offer them, replaces them with the
// registry's version once it does, and leaves them alone if they were
// edited in between.
func MarkPending(loc config.Location, items []PendingItem) error {
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return err
	}
	byPath := st.ByPath()
	for _, it := range items {
		hash, err := hashPath(it.Abs)
		if err != nil {
			return err
		}
		key := pathKey(loc.Root, it.Abs)
		byPath[key] = state.Entry{Path: key, Kind: it.Kind, Name: it.Name, Target: it.Target, Registry: it.Registry, Hash: hash, Pending: true}
	}
	st.Entries = st.Entries[:0]
	for _, e := range byPath {
		st.Entries = append(st.Entries, e)
	}
	return st.Save(loc.StatePath)
}

// Managed reports whether the file or directory is recorded in the state
// file, and whether it is only pending.
func Managed(loc config.Location, abs string) (managed, pending bool, err error) {
	st, err := state.Load(loc.StatePath)
	if err != nil {
		return false, false, err
	}
	e, ok := st.ByPath()[pathKey(loc.Root, abs)]
	return ok, e.Pending, nil
}
