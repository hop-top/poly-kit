package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LayerName names the Load layer that supplied a key's final value.
type LayerName string

// Layers, lowest precedence first (the Load order).
const (
	LayerDefault  LayerName = "default"
	LayerSystem   LayerName = "system"
	LayerUser     LayerName = "user"
	LayerProject  LayerName = "project"
	LayerExtra    LayerName = "extra"
	LayerEnv      LayerName = "env"
	LayerOverride LayerName = "override"
)

// KeyOrigin says where one key's winning value came from.
type KeyOrigin struct {
	// Key is the dotted key, as written in the files (yaml tag names).
	Key string
	// Layer is the winning layer.
	Layer LayerName
	// File is the file that supplied the value: set for system, user,
	// project and extra; empty for default, env and override.
	File string
}

// LoadWithOrigins is [Load] that also reports, per key, the layer and file
// that supplied the winning value. Load itself is unchanged.
//
// With no keys, every key that reached dst is reported, one entry per leaf
// (scalars and lists; maps are walked). With keys, only those are reported:
// a leaf key reports its own origin; a key naming a mapping reports the
// highest-precedence layer that supplied any key beneath it. A key neither
// layer set is absent from the result.
//
// Origins are read from the layers' documents, so they follow the same
// precedence as Load. A key that does not survive into dst (unknown struct
// field, omitted by yaml omitempty) is not reported. Env is detected by
// diffing dst around Options.EnvOverride: values the callback changed are
// LayerEnv. Struct and map-typed fields that merge whole (see Load) report
// the layer that supplied the surviving entry.
//
// Keys come back as written in the files, so rules that fold case (see
// [ProjectRules]) do not fold them here.
func LoadWithOrigins(dst any, opts Options, keys ...string) (map[string]KeyOrigin, error) {
	rec := &originRec{leaves: map[string]KeyOrigin{}}
	if err := load(dst, opts, rec); err != nil {
		return nil, err
	}
	final, err := leafValues(dst)
	if err != nil {
		return nil, fmt.Errorf("read merged config: %w", err)
	}
	for k := range rec.leaves {
		if _, ok := final[k]; !ok {
			delete(rec.leaves, k)
		}
	}
	return rec.query(keys), nil
}

type originRec struct {
	leaves map[string]KeyOrigin
}

// set records every leaf of doc as supplied by layer/file. Later calls win.
// Stale leaves under a replaced mapping are dropped by LoadWithOrigins when
// it prunes against the merged result.
func (r *originRec) set(doc any, layer LayerName, file string) {
	for path := range flattenDoc(doc) {
		r.setLeaf(path, layer, file)
	}
}

func (r *originRec) setLeaf(path string, layer LayerName, file string) {
	r.leaves[path] = KeyOrigin{Key: path, Layer: layer, File: file}
}

var layerRank = map[LayerName]int{
	LayerDefault: 0, LayerSystem: 1, LayerUser: 2, LayerProject: 3,
	LayerExtra: 4, LayerEnv: 5, LayerOverride: 6,
}

func (r *originRec) query(keys []string) map[string]KeyOrigin {
	out := map[string]KeyOrigin{}
	if len(keys) == 0 {
		for k, o := range r.leaves {
			out[k] = o
		}
		return out
	}
	for _, key := range keys {
		if o, ok := r.leaves[key]; ok {
			out[key] = o
			continue
		}
		// Mapping: highest layer beneath; ties keep the first key in
		// sorted order, for determinism.
		var best *KeyOrigin
		names := make([]string, 0)
		for k := range r.leaves {
			if strings.HasPrefix(k, key+".") {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for _, k := range names {
			o := r.leaves[k]
			if best == nil || layerRank[o.Layer] > layerRank[best.Layer] {
				c := o
				best = &c
			}
		}
		if best != nil {
			out[key] = KeyOrigin{Key: key, Layer: best.Layer, File: best.File}
		}
	}
	return out
}

// flattenDoc maps each leaf path of a decoded document to its value.
// Non-empty mappings are walked; scalars, lists, nulls and empty mappings
// are leaves.
func flattenDoc(doc any) map[string]any {
	out := map[string]any{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, c := range m {
				walk(joinPath(prefix, k), c)
			}
			return
		}
		if prefix != "" {
			out[prefix] = v
		}
	}
	walk("", doc)
	return out
}

func joinPath(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// leafValues is flattenDoc of dst's YAML form.
func leafValues(v any) (map[string]any, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return flattenDoc(doc), nil
}

// changedLeaves returns the paths whose value differs between a and b.
func changedLeaves(a, b map[string]any) []string {
	var out []string
	for k, bv := range b {
		if av, ok := a[k]; !ok || !reflect.DeepEqual(av, bv) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
