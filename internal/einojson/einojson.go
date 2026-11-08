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

// Package einojson is the JSON of eino: encoding/json/v2 from the standard
// library, with the semantics of encoding/json (DefaultOptionsV1) and the
// escaping sonic's default configuration had: '<', '>', '&', U+2028 and
// U+2029 are written as they are. Map keys are written sorted.
package einojson

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
)

// Options are the options every eino read and write uses.
var Options = jsonv2.JoinOptions(
	json.DefaultOptionsV1(),
	jsontext.EscapeForHTML(false),
	jsontext.EscapeForJS(false),
)

// Marshal writes v.
func Marshal(v any) ([]byte, error) {
	return jsonv2.Marshal(v, Options)
}

// MarshalString writes v as a string.
func MarshalString(v any) (string, error) {
	b, err := jsonv2.Marshal(v, Options)
	return string(b), err
}

// Unmarshal reads data into v.
func Unmarshal(data []byte, v any) error {
	return jsonv2.Unmarshal(data, v, Options)
}

// UnmarshalString reads s into v.
func UnmarshalString(s string, v any) error {
	return jsonv2.Unmarshal([]byte(s), v, Options)
}

// ErrNotFound is reported by ValueAt when a key is missing or a value on
// the path is not an object.
var ErrNotFound = errors.New("einojson: value not found")

// ValueAt returns the raw JSON of the value found by following keys through
// nested objects of data (the first member of each name), as written in
// data. data is read only as far as the value.
func ValueAt(data string, keys []string) ([]byte, error) {
	dec := jsontext.NewDecoder(bytes.NewBufferString(data), Options)
	for _, key := range keys {
		if dec.PeekKind() != '{' {
			if _, err := dec.ReadToken(); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %q is not in an object", ErrNotFound, key)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		for {
			if dec.PeekKind() == '}' {
				return nil, fmt.Errorf("%w: key %q", ErrNotFound, key)
			}
			name, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			if name.String() == key {
				break
			}
			if err := dec.SkipValue(); err != nil {
				return nil, err
			}
		}
	}
	value, err := dec.ReadValue()
	if err != nil {
		return nil, err
	}
	return bytes.Clone(value), nil
}
