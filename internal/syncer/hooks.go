package syncer

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/ojson"
	"github.com/graemer-org/spellbook/internal/state"
	"github.com/graemer-org/spellbook/internal/target"
)

// hooksKey is the key that maps event names to hook entries, both in
// Claude Code's settings and in a Copilot hooks file.
const hooksKey = "hooks"

// hookWant is a hook entry that should be present in one file.
type hookWant struct {
	id       string
	name     string
	target   string
	registry string
	commit   string
	event    string
	// entry is the hook entry as canonical JSON.
	entry []byte
}

// hookFile is a parsed hooks file: its events in order, and the entries of
// each event.
type hookFile struct {
	file    *ojson.Object
	hooks   *ojson.Object
	entries map[string][]json.RawMessage
}

func readHookFile(path string) (*hookFile, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := ojson.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
	}
	hf := &hookFile{file: file, hooks: &ojson.Object{}, entries: map[string][]json.RawMessage{}}
	if raw, ok := file.Get(hooksKey); ok {
		if hf.hooks, err = ojson.Parse(raw); err != nil {
			return nil, fmt.Errorf("%s: %q is not an object, so the file was left alone", path, hooksKey)
		}
	}
	for _, event := range hf.hooks.Keys() {
		raw, _ := hf.hooks.Get(event)
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("%s: %q is not a list, so the file was left alone", path, hooksKey+"."+event)
		}
		hf.entries[event] = list
	}
	return hf, nil
}

// find returns the event and position of the entry with the given id.
func (hf *hookFile) find(id string) (string, int, bool) {
	for _, event := range hf.hooks.Keys() {
		for i, raw := range hf.entries[event] {
			if target.HookID(raw) == id {
				return event, i, true
			}
		}
	}
	return "", 0, false
}

func (hf *hookFile) hash(id string) (string, bool) {
	event, i, ok := hf.find(id)
	if !ok {
		return "", false
	}
	canonical, err := ojson.Canonical(hf.entries[event][i])
	if err != nil {
		return "", true
	}
	return hashBytes(canonical), true
}

func (hf *hookFile) remove(id string) {
	if event, i, ok := hf.find(id); ok {
		hf.entries[event] = append(hf.entries[event][:i:i], hf.entries[event][i+1:]...)
	}
}

func (hf *hookFile) add(event string, entry []byte) {
	if _, known := hf.hooks.Get(event); !known {
		hf.hooks.Set(event, json.RawMessage("[]"))
	}
	hf.entries[event] = append(hf.entries[event], json.RawMessage(entry))
}

// write stores the file, or removes it when nothing is left in it.
func (hf *hookFile) write(place target.HookPlace) error {
	for _, event := range hf.hooks.Keys() {
		if len(hf.entries[event]) == 0 {
			hf.hooks.Remove(event)
			continue
		}
		raw, err := json.Marshal(hf.entries[event])
		if err != nil {
			return err
		}
		hf.hooks.Set(event, raw)
	}
	empty := hf.hooks.Len() == 0
	if empty {
		hf.file.Remove(hooksKey)
	} else {
		if place.Owned || place.Version {
			if _, ok := hf.file.Get("version"); !ok {
				fresh := &ojson.Object{}
				fresh.Set("version", json.RawMessage("1"))
				for _, k := range hf.file.Keys() {
					v, _ := hf.file.Get(k)
					fresh.Set(k, v)
				}
				hf.file = fresh
			}
		}
		hf.file.Set(hooksKey, hf.hooks.Marshal())
	}
	if _, versioned := hf.file.Get("version"); empty && place.Version && versioned && hf.file.Len() == 1 {
		// Only the version is left, and that says nothing on its own.
		hf.file.Remove("version")
	}
	if (place.Owned && empty) || hf.file.Len() == 0 {
		if err := os.Remove(place.File); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	out, err := hf.file.Indented()
	if err != nil {
		return err
	}
	return writeFile(place.File, out)
}

// applyHooks brings the hook entries of every hooks file in line. An entry
// is Spellbook's when its command ends in the marker comment; every other
// entry, and every other setting in the file, is left as it is.
func (r *run) applyHooks(want map[target.HookPlace][]hookWant) error {
	places := map[target.HookPlace]bool{}
	for p := range want {
		places[p] = true
	}
	recorded := map[string][]state.Entry{}
	for key, e := range r.old {
		if e.Kind != KindHook {
			continue
		}
		i := strings.LastIndex(key, "#")
		if i < 0 {
			continue
		}
		abs := absPath(r.loc.Root, key[:i])
		recorded[abs] = append(recorded[abs], e)
		// The target of the entry says which form the file has.
		if place, err := target.HookFor(e.Target, r.loc); err == nil && place.File == abs {
			places[place] = true
		}
	}
	ordered := make([]target.HookPlace, 0, len(places))
	for p := range places {
		ordered = append(ordered, p)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].File < ordered[j].File })

	for _, place := range ordered {
		fileKey := pathKey(r.loc.Root, place.File)
		hf, err := readHookFile(place.File)
		if err != nil {
			return err
		}
		changed := false
		keep := map[string]bool{}
		for _, w := range want[place] {
			keep[w.id] = true
			key := fileKey + "#" + w.id
			c := r.counts(w.target)
			wantHash := hashBytes(w.entry)
			entry := state.Entry{Path: key, Kind: KindHook, Name: w.name, Target: w.target, Registry: w.registry, Commit: w.commit, Hash: wantHash}
			prev, managed := r.old[key]
			onDisk, exists := hf.hash(w.id)
			switch {
			case !exists:
				c.Installed++
			case onDisk == wantHash:
				c.Unchanged++
				r.entries = append(r.entries, entry)
				continue
			case managed && onDisk != prev.Hash && !r.opts.Force:
				c.Skipped++
				r.warnf("%s: skipped hook %q: it was edited in %s (use --force to overwrite)", w.target, w.name, place.File)
				r.entries = append(r.entries, prev)
				continue
			default:
				c.Updated++
			}
			hf.remove(w.id)
			hf.add(w.event, w.entry)
			changed = true
			r.entries = append(r.entries, entry)
		}
		for _, e := range recorded[place.File] {
			id := e.Path[strings.LastIndex(e.Path, "#")+1:]
			if keep[id] {
				continue
			}
			onDisk, exists := hf.hash(id)
			switch {
			case r.failed[e.Registry]:
				r.entries = append(r.entries, e)
			case !exists:
				// Already gone.
			case onDisk != e.Hash && !r.opts.Force:
				r.counts(e.Target).Skipped++
				r.warnf("%s: kept hook %q: it was edited in %s (use --force to remove)", e.Target, e.Name, place.File)
				r.entries = append(r.entries, e)
			default:
				hf.remove(id)
				changed = true
				r.counts(e.Target).Removed++
			}
		}
		if changed && !r.opts.DryRun {
			if err := hf.write(place); err != nil {
				return err
			}
		}
	}
	return nil
}
