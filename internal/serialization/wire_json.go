/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package serialization

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// The checkpoint wire (internalStruct and valueType) is written and read as
// jsontext tokens, without reflection. The bytes are those encoding/json/v2
// writes for the tagged structs with the caller's options (fields in
// declaration order, omitempty, map members in map order); reading follows
// encoding/json: field names matched exactly or else without case, unknown
// members skipped, the last of repeated members kept, null leaving a field
// as it is (a RawMessage keeps "null").

func writeName(enc *jsontext.Encoder, name string) error {
	return enc.WriteToken(jsontext.String(name))
}

// MarshalJSONTo writes the node.
func (is *internalStruct) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if is.Type != nil {
		if err := writeName(enc, "Type"); err != nil {
			return err
		}
		if err := is.Type.MarshalJSONTo(enc); err != nil {
			return err
		}
	}
	if len(is.JSONValue) > 0 {
		if err := writeName(enc, "JSONValue"); err != nil {
			return err
		}
		if err := enc.WriteValue(jsontext.Value(is.JSONValue)); err != nil {
			return err
		}
	}
	if len(is.MapValues) > 0 {
		if err := writeName(enc, "MapValues"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		for k, v := range is.MapValues {
			if err := writeName(enc, k); err != nil {
				return err
			}
			if err := writeNode(enc, v); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndObject); err != nil {
			return err
		}
	}
	if len(is.SliceValues) > 0 {
		if err := writeName(enc, "SliceValues"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, v := range is.SliceValues {
			if err := writeNode(enc, v); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

func writeNode(enc *jsontext.Encoder, is *internalStruct) error {
	if is == nil {
		return enc.WriteToken(jsontext.Null)
	}
	return is.MarshalJSONTo(enc)
}

// MarshalJSONTo writes the type.
func (vt *valueType) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if vt.PointerNum != 0 {
		if err := writeName(enc, "PointerNum"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.Uint(uint64(vt.PointerNum))); err != nil {
			return err
		}
	}
	for _, f := range [...]struct {
		name  string
		value string
	}{{"SimpleType", vt.SimpleType}, {"StructType", vt.StructType}} {
		if f.value == "" {
			continue
		}
		if err := writeName(enc, f.name); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.String(f.value)); err != nil {
			return err
		}
	}
	for _, f := range [...]struct {
		name  string
		value *valueType
	}{{"MapKeyType", vt.MapKeyType}, {"MapValueType", vt.MapValueType}, {"SliceValueType", vt.SliceValueType}} {
		if f.value == nil {
			continue
		}
		if err := writeName(enc, f.name); err != nil {
			return err
		}
		if err := f.value.MarshalJSONTo(enc); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// fieldIs reports whether the quoted member name raw names field, exactly
// or else without case, as encoding/json matches field names.
func fieldIs(raw jsontext.Value, field string) bool {
	if bytes.IndexByte(raw, '\\') < 0 {
		name := raw[1 : len(raw)-1]
		return string(name) == field || strings.EqualFold(string(name), field)
	}
	name, err := jsontext.AppendUnquote(nil, raw)
	return err == nil && strings.EqualFold(string(name), field)
}

// readObjectStart reads the start of an object; null reports done.
func readObjectStart(dec *jsontext.Decoder, what string) (done bool, err error) {
	switch dec.PeekKind() {
	case 'n':
		_, err = dec.ReadToken()
		return true, err
	case '{':
		_, err = dec.ReadToken()
		return false, err
	case 0:
		_, err = dec.ReadToken()
		return true, err
	default:
		v, err := dec.ReadValue()
		if err != nil {
			return true, err
		}
		return true, fmt.Errorf("json: cannot unmarshal %s into Go value of type %s", kindName(v), what)
	}
}

func kindName(v jsontext.Value) string {
	switch v.Kind() {
	case '"':
		return "string"
	case '0':
		return "number"
	case 't', 'f':
		return "bool"
	case '[':
		return "array"
	case '{':
		return "object"
	}
	return "value"
}

// stringCache keeps the strings last read for names and type keys, which
// repeat across a checkpoint (struct field names, encoded map keys, type
// names): a name read again is the same string, without allocating. It is
// a fixed table indexed by a hash of the bytes, as encoding/json/v2 keeps
// for object names.
type stringCache [256]string

func (c *stringCache) make(b []byte) string {
	if len(b) == 0 || len(b) > 64 {
		return string(b)
	}
	h := uint32(2166136261)
	for _, x := range b {
		h = (h ^ uint32(x)) * 16777619
	}
	e := &c[h&255]
	if *e != string(b) {
		*e = string(b)
	}
	return *e
}

var stringCaches = sync.Pool{New: func() any { return new(stringCache) }}

// readString reads a JSON string, through the cache when it has no escape
// and is valid UTF-8; invalid UTF-8 reads as U+FFFD, as encoding/json reads it.
func readString(dec *jsontext.Decoder, cache *stringCache) (string, error) {
	raw, err := dec.ReadValue()
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(raw, '\\') < 0 && utf8.Valid(raw) {
		return cache.make(raw[1 : len(raw)-1]), nil
	}
	// The decoder accepted the string; unquoting can only report the
	// invalid UTF-8 it replaced.
	b, _ := jsontext.AppendUnquote(nil, raw)
	return string(b), nil
}

// readNode reads a node that may be null.
func readNode(dec *jsontext.Decoder, cache *stringCache) (*internalStruct, error) {
	if dec.PeekKind() == 'n' {
		_, err := dec.ReadToken()
		return nil, err
	}
	is := &internalStruct{}
	return is, is.decode(dec, cache)
}

// UnmarshalJSONFrom reads the node.
func (is *internalStruct) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	cache := stringCaches.Get().(*stringCache)
	defer stringCaches.Put(cache)
	return is.decode(dec, cache)
}

func (is *internalStruct) decode(dec *jsontext.Decoder, cache *stringCache) error {
	done, err := readObjectStart(dec, "serialization.internalStruct")
	if done || err != nil {
		return err
	}
	for dec.PeekKind() != '}' {
		name, err := dec.ReadValue()
		if err != nil {
			return err
		}
		switch {
		case fieldIs(name, "Type"):
			if dec.PeekKind() == 'n' {
				if _, err := dec.ReadToken(); err != nil {
					return err
				}
				is.Type = nil
				continue
			}
			if is.Type == nil {
				is.Type = &valueType{}
			}
			if err := is.Type.decode(dec, cache); err != nil {
				return err
			}
		case fieldIs(name, "JSONValue"):
			v, err := dec.ReadValue()
			if err != nil {
				return err
			}
			is.JSONValue = append(json.RawMessage(nil), v...)
		case fieldIs(name, "MapValues"):
			if dec.PeekKind() == 'n' {
				if _, err := dec.ReadToken(); err != nil {
					return err
				}
				is.MapValues = nil
				continue
			}
			if done, err := readObjectStart(dec, "map[string]*serialization.internalStruct"); done || err != nil {
				if err != nil {
					return err
				}
				continue
			}
			if is.MapValues == nil {
				is.MapValues = map[string]*internalStruct{}
			}
			for dec.PeekKind() != '}' {
				key, err := readString(dec, cache)
				if err != nil {
					return err
				}
				v, err := readNode(dec, cache)
				if err != nil {
					return err
				}
				is.MapValues[key] = v
			}
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
		case fieldIs(name, "SliceValues"):
			switch dec.PeekKind() {
			case 'n':
				if _, err := dec.ReadToken(); err != nil {
					return err
				}
				is.SliceValues = nil
				continue
			case '[':
			default:
				v, err := dec.ReadValue()
				if err != nil {
					return err
				}
				return fmt.Errorf("json: cannot unmarshal %s into Go value of type []*serialization.internalStruct", kindName(v))
			}
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			values := is.SliceValues[:0]
			for dec.PeekKind() != ']' {
				v, err := readNode(dec, cache)
				if err != nil {
					return err
				}
				values = append(values, v)
			}
			if values == nil {
				values = []*internalStruct{}
			}
			is.SliceValues = values
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
		default:
			if err := dec.SkipValue(); err != nil {
				return err
			}
		}
	}
	_, err = dec.ReadToken()
	return err
}

// UnmarshalJSONFrom reads the type.
func (vt *valueType) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	cache := stringCaches.Get().(*stringCache)
	defer stringCaches.Put(cache)
	return vt.decode(dec, cache)
}

func (vt *valueType) decode(dec *jsontext.Decoder, cache *stringCache) error {
	done, err := readObjectStart(dec, "serialization.valueType")
	if done || err != nil {
		return err
	}
	for dec.PeekKind() != '}' {
		name, err := dec.ReadValue()
		if err != nil {
			return err
		}
		switch {
		case fieldIs(name, "PointerNum"):
			v, err := dec.ReadValue()
			if err != nil {
				return err
			}
			switch v.Kind() {
			case 'n':
			case '0':
				n, err := strconv.ParseUint(string(v), 10, 32)
				if err != nil {
					return fmt.Errorf("json: cannot unmarshal number %s into Go struct field valueType.PointerNum of type uint32", v)
				}
				vt.PointerNum = uint32(n)
			default:
				return fmt.Errorf("json: cannot unmarshal %s into Go struct field valueType.PointerNum of type uint32", kindName(v))
			}
		case fieldIs(name, "SimpleType"), fieldIs(name, "StructType"):
			target := &vt.SimpleType
			if fieldIs(name, "StructType") {
				target = &vt.StructType
			}
			switch dec.PeekKind() {
			case 'n':
				if _, err := dec.ReadToken(); err != nil {
					return err
				}
			case '"':
				str, err := readString(dec, cache)
				if err != nil {
					return err
				}
				*target = str
			default:
				v, err := dec.ReadValue()
				if err != nil {
					return err
				}
				return fmt.Errorf("json: cannot unmarshal %s into Go struct field of type string", kindName(v))
			}
		case fieldIs(name, "MapKeyType"), fieldIs(name, "MapValueType"), fieldIs(name, "SliceValueType"):
			target := &vt.SliceValueType
			switch {
			case fieldIs(name, "MapKeyType"):
				target = &vt.MapKeyType
			case fieldIs(name, "MapValueType"):
				target = &vt.MapValueType
			}
			if dec.PeekKind() == 'n' {
				if _, err := dec.ReadToken(); err != nil {
					return err
				}
				*target = nil
				continue
			}
			if *target == nil {
				*target = &valueType{}
			}
			if err := (*target).decode(dec, cache); err != nil {
				return err
			}
		default:
			if err := dec.SkipValue(); err != nil {
				return err
			}
		}
	}
	_, err = dec.ReadToken()
	return err
}
