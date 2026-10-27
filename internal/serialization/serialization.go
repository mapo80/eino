/*
 * Copyright 2024 CloudWeGo Authors
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
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/bytedance/sonic"
)

var m = map[string]reflect.Type{}
var rm = map[reflect.Type]string{}

func init() {
	_ = GenericRegister[int]("_eino_int")
	_ = GenericRegister[int8]("_eino_int8")
	_ = GenericRegister[int16]("_eino_int16")
	_ = GenericRegister[int32]("_eino_int32")
	_ = GenericRegister[int64]("_eino_int64")
	_ = GenericRegister[uint]("_eino_uint")
	_ = GenericRegister[uint8]("_eino_uint8")
	_ = GenericRegister[uint16]("_eino_uint16")
	_ = GenericRegister[uint32]("_eino_uint32")
	_ = GenericRegister[uint64]("_eino_uint64")
	_ = GenericRegister[float32]("_eino_float32")
	_ = GenericRegister[float64]("_eino_float64")
	_ = GenericRegister[complex64]("_eino_complex64")
	_ = GenericRegister[complex128]("_eino_complex128")
	_ = GenericRegister[uintptr]("_eino_uintptr")
	_ = GenericRegister[bool]("_eino_bool")
	_ = GenericRegister[string]("_eino_string")
	_ = GenericRegister[any]("_eino_any")
}

func GenericRegister[T any](key string) error {
	t := reflect.TypeOf((*T)(nil)).Elem()
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if nt, ok := m[key]; ok {
		return fmt.Errorf("key[%s] already registered to %s", key, nt.String())
	}
	if nk, ok := rm[t]; ok {
		return fmt.Errorf("type[%s] already registered to %s", t.String(), nk)
	}
	m[key] = t
	rm[t] = key
	return nil
}

type InternalSerializer struct{}

func (i *InternalSerializer) Marshal(v any) ([]byte, error) {
	is, err := internalMarshal(v, nil)
	if err != nil {
		return nil, err
	}

	return sonic.Marshal(is)
}

func (i *InternalSerializer) Unmarshal(data []byte, v any) error {
	val, err := unmarshal(data, reflect.TypeOf(v))
	if err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}
	return assignUnmarshaled(val, v)
}

// Parsed is the parsed form of the bytes returned by MarshalParsed: decoding
// it gives the value Unmarshal gives for those bytes. It shares no memory with
// the marshaled value and is never modified, so it can be decoded any number
// of times, also concurrently.
type Parsed struct {
	is *internalStruct
}

// MarshalParsed is Marshal that also returns the parsed form of the bytes.
func (i *InternalSerializer) MarshalParsed(v any) ([]byte, *Parsed, error) {
	is, err := internalMarshal(v, nil)
	if err != nil {
		return nil, nil, err
	}
	data, err := sonic.Marshal(is)
	if err != nil {
		return nil, nil, err
	}
	if is == nil {
		// The bytes are null, which parses to an empty node.
		is = &internalStruct{}
	}
	return data, &Parsed{is: is}, nil
}

// UnmarshalParsed is Unmarshal of the bytes p was made with, without parsing
// them again.
func (i *InternalSerializer) UnmarshalParsed(p *Parsed, v any) error {
	if p == nil || p.is == nil {
		return fmt.Errorf("failed to unmarshal: missing parsed value")
	}
	val, err := internalUnmarshal(p.is, reflect.TypeOf(v))
	if err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}
	return assignUnmarshaled(val, v)
}

// Tree is the encoded form of a value without the final byte encoding. It is
// immutable. Ordinary values share no memory with the source or decoded
// values; registered checkpoint codecs may explicitly share immutable values
// through Clone. It can stand in for the bytes of an in-memory store.
type Tree struct {
	is *internalStruct
}

// MarshalTree is Marshal without the final byte encoding.
func (i *InternalSerializer) MarshalTree(v any) (*Tree, error) {
	is, err := internalMarshalMode(v, nil, true, false)
	if err != nil {
		return nil, err
	}
	if is == nil {
		// Marshal encodes a nil tree as null, which decodes to an empty node.
		is = &internalStruct{}
	}
	return &Tree{is: is}, nil
}

// UnmarshalTree is Unmarshal of a tree made by MarshalTree.
func (i *InternalSerializer) UnmarshalTree(tree *Tree, v any) error {
	if tree == nil || tree.is == nil {
		return fmt.Errorf("failed to unmarshal: missing tree")
	}
	val, err := internalUnmarshalMode(tree.is, reflect.TypeOf(v), true, false)
	if err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}
	return assignUnmarshaled(val, v)
}

func assignUnmarshaled(val any, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("failed to unmarshal: value must be a non-nil pointer")
	}

	target := rv.Elem()
	if !target.CanSet() {
		return fmt.Errorf("failed to unmarshal: output value must be settable")
	}

	if val == nil {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}

	source := reflect.ValueOf(val)

	var set func(target, source reflect.Value) bool
	set = func(target, source reflect.Value) bool {
		if !source.IsValid() {
			target.Set(reflect.Zero(target.Type()))
			return true
		}
		if source.Type().AssignableTo(target.Type()) {
			target.Set(source)
			return true
		}

		if target.Kind() == reflect.Ptr {
			if target.IsNil() {
				if !target.CanSet() {
					return false
				}
				target.Set(reflect.New(target.Type().Elem()))
			}
			return set(target.Elem(), source)
		}

		if source.Kind() == reflect.Ptr {
			if source.IsNil() {
				target.Set(reflect.Zero(target.Type()))
				return true
			}
			return set(target, source.Elem())
		}

		if source.Type().ConvertibleTo(target.Type()) {
			target.Set(source.Convert(target.Type()))
			return true
		}

		return false
	}

	if set(target, source) {
		return nil
	}

	return fmt.Errorf("failed to unmarshal: cannot assign %s to %s", reflect.TypeOf(val), target.Type())
}

func unmarshal(data []byte, t reflect.Type) (any, error) {
	is := &internalStruct{}
	err := sonic.Unmarshal(data, is)
	if err != nil {
		return nil, err
	}
	return internalUnmarshal(is, t)
}

type internalStruct struct {
	// treeValue is private to in-memory Trees and is never serialized.
	treeType     reflect.Type
	treeValue    any
	hasTreeValue bool

	Type *valueType `json:",omitempty"`

	JSONValue json.RawMessage `json:",omitempty"`

	// map or struct
	// in map, the key is the serialized map key anyway todo: if key is string, don't serialize
	// in struct, the key is the original field name
	MapValues map[string]*internalStruct `json:",omitempty"`

	// slice
	SliceValues []*internalStruct `json:",omitempty"`
}

type valueType struct {
	PointerNum uint32 `json:",omitempty"`

	SimpleType string `json:",omitempty"`

	StructType string `json:",omitempty"`

	MapKeyType   *valueType `json:",omitempty"`
	MapValueType *valueType `json:",omitempty"`

	SliceValueType *valueType `json:",omitempty"`
}

func extractType(t reflect.Type) (*valueType, error) { return extractTypeMode(t, false) }

func extractTypeMode(t reflect.Type, tree bool) (*valueType, error) {
	if !tree {
		if codec, ok := codecFor(t); ok {
			t = codec.wire
		}
	}
	ret := &valueType{}
	for t.Kind() == reflect.Ptr {
		ret.PointerNum += 1
		t = t.Elem()
	}
	var err error
	if t.Kind() == reflect.Map {
		ret.MapKeyType, err = extractTypeMode(t.Key(), tree)
		if err != nil {
			return nil, err
		}
		ret.MapValueType, err = extractTypeMode(t.Elem(), tree)
		if err != nil {
			return nil, err
		}
	} else if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		ret.SliceValueType, err = extractTypeMode(t.Elem(), tree)
		if err != nil {
			return nil, err
		}
	} else {
		key, ok := rm[t]
		if !ok {
			return ret, fmt.Errorf("unknown type: %s", t.String())
		}
		ret.SimpleType = key
	}
	return ret, nil
}

func restoreType(vt *valueType) (reflect.Type, error) {
	if vt.SimpleType != "" {
		rt, ok := m[vt.SimpleType]
		if !ok {
			return nil, fmt.Errorf("unknown type: %s", vt.SimpleType)
		}
		return resolvePointerNum(vt.PointerNum, rt), nil
	}
	if vt.StructType != "" {
		rt, ok := m[vt.StructType]
		if !ok {
			return nil, fmt.Errorf("unknown type: %s", vt.StructType)
		}
		return resolvePointerNum(vt.PointerNum, rt), nil
	}
	if vt.MapKeyType != nil {
		rkt, err := restoreType(vt.MapKeyType)
		if err != nil {
			return nil, err
		}
		rvt, err := restoreType(vt.MapValueType)
		if err != nil {
			return nil, err
		}
		return resolvePointerNum(vt.PointerNum, reflect.MapOf(rkt, rvt)), nil
	}
	if vt.SliceValueType != nil {
		rt, err := restoreType(vt.SliceValueType)
		if err != nil {
			return nil, err
		}
		return resolvePointerNum(vt.PointerNum, reflect.SliceOf(rt)), nil
	}
	return nil, fmt.Errorf("empty value")
}

func internalMarshal(v any, fieldType reflect.Type) (*internalStruct, error) {
	return internalMarshalMode(v, fieldType, false, false)
}

func internalMarshalMode(v any, fieldType reflect.Type, tree, skipCodec bool) (*internalStruct, error) {
	if v == nil ||
		(reflect.ValueOf(v).IsZero() && fieldType != nil && fieldType.Kind() != reflect.Interface) {
		return nil, nil
	}

	if !skipCodec {
		if codec, ok := codecFor(reflect.TypeOf(v)); ok {
			// A registered typed nil already has its historical native null
			// representation. Its wire DTO need not be registered or allocated.
			rv := reflect.ValueOf(v)
			_, base := derefPointerNum(rv.Type())
			_, registered := rm[base]
			if !tree && rv.Kind() == reflect.Ptr && rv.IsNil() && registered {
				return internalMarshalMode(v, fieldType, false, true)
			}
			if tree {
				if codec.clone != nil {
					cloned, err := codec.clone(v)
					if err != nil {
						return nil, err
					}
					return &internalStruct{treeValue: cloned, hasTreeValue: true}, nil
				}
			} else {
				wire, err := codec.encode(v)
				if err != nil {
					return nil, err
				}
				wireField := fieldType
				if fieldType != nil && fieldType.Kind() != reflect.Interface {
					wireField = codec.wire
				}
				return internalMarshalMode(wire, wireField, false, true)
			}
		}
	}
	ret := &internalStruct{}
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	typeUnspecific := fieldType == nil || fieldType.Kind() == reflect.Interface
	if tree && typeUnspecific {
		ret.treeType = rt
		typeUnspecific = false
	}

	var pointerNum uint32
	for rt.Kind() == reflect.Ptr {
		pointerNum++
		if !rv.IsNil() {
			rv = rv.Elem()
			rt = rt.Elem()
			continue
		}
		for rt.Kind() == reflect.Ptr {
			rt = rt.Elem()
		}
		if typeUnspecific {
			// need type registered
			key, ok := rm[rt]
			if !ok {
				return nil, fmt.Errorf("unknown type: %v", rt)
			}
			ret.Type = &valueType{
				PointerNum: pointerNum,
				SimpleType: key,
			}
		}
		ret.JSONValue = json.RawMessage("null")
		return ret, nil
	}

	switch rt.Kind() {
	case reflect.Struct:
		if typeUnspecific {
			// need type registered
			key, ok := rm[rt]
			if !ok {
				return nil, fmt.Errorf("unknown type: %v", rt)
			}

			if checkMarshaler(rt) {
				ret.Type = &valueType{
					PointerNum: pointerNum,
					SimpleType: key,
				}
			} else {
				ret.Type = &valueType{
					PointerNum: pointerNum,
					StructType: key,
				}
			}
		}

		if checkMarshaler(rt) {
			// Use rv.Addr() when possible so that pointer-receiver MarshalJSON methods
			// are callable. rv is addressable when obtained from pointer dereference.
			// When not addressable, copy into an addressable temporary.
			var marshalTarget any
			if rv.CanAddr() {
				marshalTarget = rv.Addr().Interface()
			} else {
				tmp := reflect.New(rt)
				tmp.Elem().Set(rv)
				marshalTarget = tmp.Interface()
			}
			jsonBytes, err := json.Marshal(marshalTarget)
			if err != nil {
				return nil, err
			}
			ret.JSONValue = jsonBytes
			return ret, nil
		}

		ret.MapValues = make(map[string]*internalStruct)

		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			// only handle exported fields
			if field.PkgPath == "" {
				k := field.Name
				v := rv.Field(i)

				internalValue, err := internalMarshalMode(v.Interface(), field.Type, tree, false)
				if err != nil {
					return nil, err
				}

				ret.MapValues[k] = internalValue
			}
		}

		return ret, nil
	case reflect.Map:
		if typeUnspecific {
			var err error
			ret.Type = &valueType{
				PointerNum: pointerNum,
			}
			// map key type
			ret.Type.MapKeyType, err = extractTypeMode(rt.Key(), tree)
			if err != nil {
				return nil, err
			}

			// map value type
			ret.Type.MapValueType, err = extractTypeMode(rt.Elem(), tree)
			if err != nil {
				return nil, err
			}
		}

		ret.MapValues = make(map[string]*internalStruct)

		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key()
			v := iter.Value()

			internalValue, err := internalMarshalMode(v.Interface(), rt.Elem(), tree, false)
			if err != nil {
				return nil, err
			}

			key, err := marshalMapKey(k.Interface())
			if err != nil {
				return nil, fmt.Errorf("marshaling map key[%v] fail: %v", k.Interface(), err)
			}
			ret.MapValues[key] = internalValue
		}

		return ret, nil
	case reflect.Slice, reflect.Array:
		if typeUnspecific {
			var err error
			ret.Type = &valueType{PointerNum: pointerNum}
			ret.Type.SliceValueType, err = extractTypeMode(rt.Elem(), tree)
			if err != nil {
				return nil, err
			}
		}

		length := rv.Len()
		ret.SliceValues = make([]*internalStruct, length)

		for i := 0; i < length; i++ {
			internalValue, err := internalMarshalMode(rv.Index(i).Interface(), rt.Elem(), tree, false)
			if err != nil {
				return nil, err
			}
			ret.SliceValues[i] = internalValue
		}

		return ret, nil

	default:
		if typeUnspecific {
			key, ok := rm[rv.Type()]
			if !ok {
				return nil, fmt.Errorf("unknown type: %v", rt)
			}
			ret.Type = &valueType{
				PointerNum: pointerNum,
				SimpleType: key,
			}
		}

		jsonBytes, err := json.Marshal(rv.Interface())
		if err != nil {
			return nil, err
		}
		ret.JSONValue = jsonBytes
		return ret, nil
	}
}

// marshalMapKey encodes a map key. A plain string key is encoded by
// appendKeyString with the same bytes as sonic.MarshalString, without
// taking sonic's pooled encoder; other keys go through sonic as before.
func marshalMapKey(k any) (string, error) {
	if str, ok := k.(string); ok {
		return string(appendKeyString(make([]byte, 0, len(str)+2), str)), nil
	}
	return sonic.MarshalString(k)
}

// appendKeyString appends str as a JSON string the way sonic's default
// configuration does: '"', '\\' and control characters are escaped (\t,
// \n and \r by name, the others as \u00XX); everything else, including
// '<', '>', '&', U+2028, U+2029 and invalid UTF-8, is copied as is.
func appendKeyString(dst []byte, str string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(str); i++ {
		c := str[i]
		switch {
		case c == '"' || c == '\\':
			dst = append(dst, '\\', c)
		case c == '\t':
			dst = append(dst, '\\', 't')
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c == '\r':
			dst = append(dst, '\\', 'r')
		case c < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '"')
}

// unmarshalLeaf decodes the JSON of one leaf value or map key. Leaves and map
// keys are small documents; encoding/json decodes them at a cost proportional
// to the leaf, while sonic's decoder takes a pooled parser with fixed
// megabyte buffers that is allocated again after every garbage collection
// empties the pool. On valid UTF-8 both decoders give the same value. sonic
// keeps invalid UTF-8 as is where encoding/json replaces it with U+FFFD, so
// such input still goes through sonic.
func unmarshalLeaf(data []byte, v any) error {
	if utf8.Valid(data) {
		return json.Unmarshal(data, v)
	}
	return sonic.Unmarshal(data, v)
}

func internalUnmarshal(v *internalStruct, typ reflect.Type) (any, error) {
	return internalUnmarshalMode(v, typ, false, false)
}

func internalUnmarshalMode(v *internalStruct, typ reflect.Type, tree, skipCodec bool) (any, error) {
	if v == nil {
		return nil, nil
	}
	if tree && v.hasTreeValue {
		if v.treeValue == nil {
			return nil, nil
		}
		codec, ok := codecFor(reflect.TypeOf(v.treeValue))
		if !ok || codec.clone == nil {
			return nil, fmt.Errorf("missing checkpoint clone codec for %T", v.treeValue)
		}
		return codec.clone(v.treeValue)
	}
	if tree && v.treeType != nil {
		typ = v.treeType
		if typ.Kind() == reflect.Ptr && string(v.JSONValue) == "null" {
			return reflect.Zero(typ).Interface(), nil
		}
	}
	if !tree && !skipCodec {
		native := typ
		for native.Kind() == reflect.Ptr {
			if _, ok := codecFor(native); ok {
				break
			}
			native = native.Elem()
		}
		if v.Type != nil {
			restored, err := restoreType(v.Type)
			if err != nil {
				return nil, err
			}
			// A concrete destination can represent a historical wire type.
			if _, registered := codecFor(native); !registered {
				native = restored
			}
		}
		if codec, ok := codecFor(native); ok {
			if native.Kind() == reflect.Ptr && string(v.JSONValue) == "null" {
				return reflect.Zero(native).Interface(), nil
			}
			// The outer tag identifies the native registration; the codec
			// supplies the concrete historical field layout.
			wireNode := *v
			wireNode.Type = nil
			wire, err := internalUnmarshalMode(&wireNode, codec.wire, false, true)
			if err != nil {
				return nil, err
			}
			return codec.decode(wire)
		}
		// Container tags describe the durable wire element types. A concrete
		// destination still supplies the native types to restore at each leaf.
		if v.Type != nil && checkpointContainerHasCodec(typ) {
			node := *v
			node.Type = nil
			return internalSpecificTypeUnmarshalMode(&node, typ, false)
		}
	}

	if v.Type == nil {
		// specific type
		if checkMarshaler(typ) {
			pv := reflect.New(typ)
			err := json.Unmarshal(v.JSONValue, pv.Interface())
			if err != nil {
				return nil, err
			}
			return pv.Elem().Interface(), nil
		}
		return internalSpecificTypeUnmarshalMode(v, typ, tree)
	}

	if len(v.Type.SimpleType) != 0 {
		// based type
		t, ok := m[v.Type.SimpleType]
		if !ok {
			return nil, fmt.Errorf("unknown type key: %v", v.Type)
		}
		pResult := reflect.New(resolvePointerNum(v.Type.PointerNum, t))
		err := unmarshalLeaf(v.JSONValue, pResult.Interface())
		if err != nil {
			return nil, fmt.Errorf("unmarshal type[%s] fail: %v, data: %s", t.String(), err, string(v.JSONValue))
		}
		return pResult.Elem().Interface(), nil
	}

	if len(v.Type.StructType) > 0 {
		// struct
		rt, ok := m[v.Type.StructType]
		if !ok {
			return nil, fmt.Errorf("unknown type key: %v", v.Type.StructType)
		}
		result, dResult := createValueFromType(resolvePointerNum(v.Type.PointerNum, rt))

		err := setStructFieldsMode(dResult, v.MapValues, tree)
		if err != nil {
			return nil, err
		}

		return result.Interface(), nil
	}

	if v.Type.MapKeyType != nil {
		// map
		rkt, err := restoreType(v.Type.MapKeyType)
		if err != nil {
			return nil, err
		}
		rvt, err := restoreType(v.Type.MapValueType)
		if err != nil {
			return nil, err
		}

		result, dResult := createValueFromType(reflect.MapOf(rkt, rvt))
		err = setMapKVsMode(dResult, v.MapValues, tree)
		if err != nil {
			return nil, err
		}
		return result.Interface(), nil
	}

	// slice
	rvt, err := restoreType(v.Type.SliceValueType)
	if err != nil {
		return nil, err
	}

	result, dResult := createValueFromType(reflect.SliceOf(rvt))
	err = setSliceElemsMode(dResult, v.SliceValues, tree)
	if err != nil {
		return nil, err
	}
	return result.Interface(), nil
}

func internalSpecificTypeUnmarshal(is *internalStruct, typ reflect.Type) (any, error) {
	return internalSpecificTypeUnmarshalMode(is, typ, false)
}

func internalSpecificTypeUnmarshalMode(is *internalStruct, typ reflect.Type, tree bool) (any, error) {
	_, dtyp := derefPointerNum(typ)
	result, dResult := createValueFromType(typ)

	if dtyp.Kind() == reflect.Struct {
		err := setStructFieldsMode(dResult, is.MapValues, tree)
		if err != nil {
			return nil, err
		}
		return result.Interface(), nil
	} else if dtyp.Kind() == reflect.Map {
		err := setMapKVsMode(dResult, is.MapValues, tree)
		if err != nil {
			return nil, err
		}
		return result.Interface(), nil
	} else if dtyp.Kind() == reflect.Array || dtyp.Kind() == reflect.Slice {
		err := setSliceElemsMode(dResult, is.SliceValues, tree)
		if err != nil {
			return nil, err
		}
		return result.Interface(), nil
	}
	// simple type
	v := reflect.New(typ)
	err := unmarshalLeaf(is.JSONValue, v.Interface())
	if err != nil {
		return nil, fmt.Errorf("unmarshal type[%s] fail: %v", typ.String(), err)
	}
	return v.Elem().Interface(), nil
}

func setSliceElems(dResult reflect.Value, values []*internalStruct) error {
	return setSliceElemsMode(dResult, values, false)
}

func setSliceElemsMode(dResult reflect.Value, values []*internalStruct, tree bool) error {
	t := dResult.Type()

	// Handle arrays differently from slices
	// Arrays have fixed size and cannot use reflect.Append
	if dResult.Kind() == reflect.Array {
		for i, internalValue := range values {
			if i >= dResult.Len() {
				return fmt.Errorf("array index out of bounds: trying to set index %d in array of length %d", i, dResult.Len())
			}
			value, err := internalUnmarshalMode(internalValue, t.Elem(), tree, false)
			if err != nil {
				return fmt.Errorf("unmarshal array[%s] element %d fail: %v", t.Elem(), i, err)
			}
			if value == nil {
				dResult.Index(i).Set(reflect.Zero(t.Elem()))
			} else {
				dResult.Index(i).Set(reflect.ValueOf(value))
			}
		}
		return nil
	}

	// For slices, use Append as before
	for _, internalValue := range values {
		value, err := internalUnmarshalMode(internalValue, t.Elem(), tree, false)
		if err != nil {
			return fmt.Errorf("unmarshal slice[%s] fail: %v", t.Elem(), err)
		}
		if value == nil {
			// empty value
			dResult.Set(reflect.Append(dResult, reflect.New(t.Elem()).Elem()))
		} else {
			dResult.Set(reflect.Append(dResult, reflect.ValueOf(value)))
		}
	}
	return nil
}

func setMapKVs(dResult reflect.Value, values map[string]*internalStruct) error {
	return setMapKVsMode(dResult, values, false)
}

func setMapKVsMode(dResult reflect.Value, values map[string]*internalStruct, tree bool) error {
	t := dResult.Type()
	for marshaledMapKey, internalValue := range values {
		prkv := reflect.New(t.Key())
		err := unmarshalLeaf([]byte(marshaledMapKey), prkv.Interface())
		if err != nil {
			return fmt.Errorf("unmarshal map key[%v] to type[%s] fail: %v", marshaledMapKey, t.Key(), err)
		}

		value, err := internalUnmarshalMode(internalValue, t.Elem(), tree, false)
		if err != nil {
			return fmt.Errorf("unmarshal map value fail: %v", err)
		}
		if value == nil {
			dResult.SetMapIndex(prkv.Elem(), reflect.New(t.Elem()).Elem())
		} else {
			dResult.SetMapIndex(prkv.Elem(), reflect.ValueOf(value))
		}
	}
	return nil
}

func setStructFields(dResult reflect.Value, values map[string]*internalStruct) error {
	return setStructFieldsMode(dResult, values, false)
}

func setStructFieldsMode(dResult reflect.Value, values map[string]*internalStruct, tree bool) error {
	t := dResult.Type()
	for k, internalValue := range values {
		sf, ok := t.FieldByName(k)
		if !ok {
			continue
		}
		value, err := internalUnmarshalMode(internalValue, sf.Type, tree, false)
		if err != nil {
			return fmt.Errorf("unmarshal map field[%v] fail: %v", k, err)
		}
		err = setStructField(t, dResult, k, value)
		if err != nil {
			return err
		}
	}
	return nil
}

func setStructField(t reflect.Type, s reflect.Value, fieldName string, val any) error {
	field := s.FieldByName(fieldName)
	if !field.CanSet() {
		return fmt.Errorf("unmarshal map fail, can not set field %v", fieldName)
	}
	if val == nil {
		rft, ok := t.FieldByName(fieldName)
		if !ok {
			return fmt.Errorf("unmarshal map fail, cannot find field: %v", fieldName)
		}
		field.Set(reflect.New(rft.Type).Elem())
	} else {
		field.Set(reflect.ValueOf(val))
	}
	return nil
}

func resolvePointerNum(pointerNum uint32, t reflect.Type) reflect.Type {
	for i := uint32(0); i < pointerNum; i++ {
		t = reflect.PointerTo(t)
	}
	return t
}

func derefPointerNum(t reflect.Type) (uint32, reflect.Type) {
	var ptrCount uint32 = 0

	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
		ptrCount++
	}

	return ptrCount, t
}

func createValueFromType(t reflect.Type) (value reflect.Value, derefValue reflect.Value) {
	value = reflect.New(t).Elem()

	derefValue = value
	for derefValue.Kind() == reflect.Ptr {
		if derefValue.IsNil() {
			derefValue.Set(reflect.New(derefValue.Type().Elem()))
		}
		derefValue = derefValue.Elem()
	}

	if derefValue.Kind() == reflect.Map && derefValue.IsNil() {
		derefValue.Set(reflect.MakeMap(derefValue.Type()))
	}

	// Use Len() == 0 instead of IsNil() for slices to avoid panic
	// IsNil() can panic on uninitialized slice values created via reflect.New().Elem()
	if derefValue.Kind() == reflect.Slice {
		if derefValue.Len() == 0 && derefValue.Cap() == 0 {
			derefValue.Set(reflect.MakeSlice(derefValue.Type(), 0, 0))
		}
	}
	// Arrays cannot be nil and don't need initialization

	return value, derefValue
}

var marshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
var unmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

func checkMarshaler(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if (t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType)) &&
		(t.Implements(unmarshalerType) || reflect.PointerTo(t).Implements(unmarshalerType)) {
		return true
	}
	return false
}
