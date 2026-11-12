// Package ojson edits JSON objects in files that other programs and people
// also edit, keeping the order of the keys it does not touch.
package ojson

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Object is a JSON object that remembers the order of its keys.
type Object struct {
	keys   []string
	values map[string]json.RawMessage
}

// Parse reads a JSON object. Empty input is an empty object.
func Parse(data []byte) (*Object, error) {
	o := &Object{values: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("expected a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.Set(key, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

// Keys returns the keys in their order.
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Len returns the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// Get returns the value of key.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	raw, ok := o.values[key]
	return raw, ok
}

// Set adds key at the end or replaces its value in place.
func (o *Object) Set(key string, raw json.RawMessage) {
	if o.values == nil {
		o.values = map[string]json.RawMessage{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = raw
}

// Remove deletes key.
func (o *Object) Remove(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Marshal returns the object as compact JSON.
func (o *Object) Marshal() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		b.Write(o.values[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// Indented returns the object as indented JSON with a final newline.
func (o *Object) Indented() ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, o.Marshal(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// Canonical returns raw re-encoded with sorted keys and no insignificant
// whitespace, so two spellings of the same value compare equal.
func Canonical(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
