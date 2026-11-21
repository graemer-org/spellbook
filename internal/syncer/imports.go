package syncer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/gitx"
	"github.com/graemer-org/spellbook/internal/registry"
)

// ImportStatus says how one import of a registry relates to the repository
// it takes from.
type ImportStatus struct {
	// Index is the position of the import in registry.toml, from zero.
	Index int
	URL   string
	Ref   string
	// Follows is set for an import that is not pinned and names what it
	// follows: "the default branch" or "branch <name>".
	Follows string
	// From is the commit a pinned import is at, To the newest commit of
	// the repository's default branch.
	From, To string
	// Added, Removed and Changed name what the import would bring
	// differently at To, in the form Diff uses.
	Added, Removed, Changed []string
	// Err is set when the import could not be compared.
	Err error
}

// Behind reports whether the repository moved on since the pinned commit.
func (s ImportStatus) Behind() bool {
	return s.Follows == "" && s.Err == nil && !strings.HasPrefix(s.To, s.From)
}

// Outdated reports whether what the import brings differs at the newest
// commit. A repository that only changed elsewhere does not make an import
// outdated.
func (s ImportStatus) Outdated() bool {
	return s.Behind() && len(s.Added)+len(s.Removed)+len(s.Changed) > 0
}

// Movable reports whether the pin can move to the newest commit without
// the registry losing something it offers today.
func (s ImportStatus) Movable() bool {
	return s.Outdated() && len(s.Removed) == 0
}

// CompareImports checks every import of the registry in dir against the
// repository it comes from. An import pinned to a commit or a tag is
// compared with the newest commit of the default branch. It changes
// nothing in dir.
func CompareImports(ctx context.Context, dir string) ([]ImportStatus, error) {
	m, _, err := registry.LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("%s has no %s", dir, registry.ManifestFile)
	}
	var out []ImportStatus
	for i, imp := range m.Imports {
		st := ImportStatus{Index: i, URL: imp.URL, Ref: imp.Ref}
		st.Err = compareImport(ctx, imp, &st)
		out = append(out, st)
	}
	return out, nil
}

func compareImport(ctx context.Context, imp registry.Import, st *ImportStatus) error {
	if imp.Ref == "" {
		st.Follows = "the default branch"
		return nil
	}
	kind, commit, err := gitx.RefKind(ctx, imp.URL, imp.Ref)
	if err != nil {
		return err
	}
	if kind == gitx.RefBranch {
		st.Follows = "branch " + imp.Ref
		return nil
	}
	st.From = commit
	if st.To, err = gitx.RemoteHead(ctx, imp.URL, ""); err != nil {
		return err
	}
	if st.To == "" {
		return fmt.Errorf("the repository has no default branch")
	}
	if !st.Behind() {
		return nil
	}
	// Both fetches use one checkout, so the first is hashed before the
	// second replaces it.
	at := func(ref string) (map[string]string, error) {
		imp.Ref = ref
		snap, err := registry.FetchImport(ctx, imp)
		if err != nil {
			return nil, err
		}
		return hashes(snap)
	}
	old, err := at(st.From)
	if err != nil {
		return fmt.Errorf("fetch the pinned commit: %w", err)
	}
	cur, err := at(st.To)
	if err != nil {
		return err
	}
	for name, h := range cur {
		if prev, ok := old[name]; !ok {
			st.Added = append(st.Added, name)
		} else if prev != h {
			st.Changed = append(st.Changed, name)
		}
	}
	for name := range old {
		if _, ok := cur[name]; !ok {
			st.Removed = append(st.Removed, name)
		}
	}
	sort.Strings(st.Added)
	sort.Strings(st.Removed)
	sort.Strings(st.Changed)
	return nil
}

// UpdateImports moves the pin of every import that can move to its newest
// commit, in the registry.toml in dir, and returns how many it moved.
func UpdateImports(dir string, statuses []ImportStatus) (int, error) {
	refs := map[int]string{}
	for _, s := range statuses {
		if s.Movable() {
			refs[s.Index] = s.To
		}
	}
	if len(refs) == 0 {
		return 0, nil
	}
	return len(refs), registry.SetImportRefs(dir, refs)
}
