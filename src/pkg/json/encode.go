// Copyright 2010 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package json implements encoding and decoding of JSON objects as defined in
// RFC 4627.
//
// See "JSON and Go" for an introduction to this package:
// http://blog.golang.org/2011/01/json-and-go.html
package json

import (
	"bytes"
	"encoding/base64"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"unicode"
	"utf8"
)

// Marshal returns the JSON encoding of v.
//
// Marshal traverses the value v recursively.
// If an encountered value implements the Marshaler interface,
// Marshal calls its MarshalJSON method to produce JSON.
//
// Otherwise, Marshal uses the following type-dependent default encodings:
//
// Boolean values encode as JSON booleans.
//
// Floating point and integer values encode as JSON numbers.
//
// String values encode as JSON strings, with each invalid UTF-8 sequence
// replaced by the encoding of the Unicode replacement character U+FFFD.
//
// Array and slice values encode as JSON arrays, except that
// []byte encodes as a base64-encoded string.
//
// Struct values encode as JSON objects. Each exported struct field
// becomes a member of the object unless the field is empty and its tag
// specifies the "omitempty" option. The empty values are false, 0, any
// nil pointer or interface value, and any array, slice, map, or string of
// length zero. The object's default key string is the struct field name
// but can be specified in the struct field's tag value. The "json" key in
// struct field's tag value is the key name, followed by an optional comma
// and options. Examples:
//
//   // Specifies that Field appears in JSON as key "myName"
//   Field int `json:"myName"`
//
//   // Specifies that Field appears in JSON as key "myName" and
//   // the field is omitted from the object if its value is empty,
//   // as defined above.
//   Field int `json:"myName,omitempty"`
//
//   // Field appears in JSON as key "Field" (the default), but
//   // the field is skipped if empty.
//   // Note the leading comma.
//   Field int `json:",omitempty"`
//
// The "string" option signals that a field is stored as JSON inside a
// JSON-encoded string.  This extra level of encoding is sometimes
// used when communicating with JavaScript programs:
//
//    Int64String int64 `json:",string"`
//
// The key name will be used if it's a non-empty string consisting of
// only Unicode letters, digits, dollar signs, hyphens, and underscores.
//
// Map values encode as JSON objects.
// The map's key type must be string; the object keys are used directly
// as map keys.
//
// Pointer values encode as the value pointed to.
// A nil pointer encodes as the null JSON object.
//
// Interface values encode as the value contained in the interface.
// A nil interface value encodes as the null JSON object.
//
// Channel, complex, and function values cannot be encoded in JSON.
// Attempting to encode such a value causes Marshal to return
// an InvalidTypeError.
//
// JSON cannot represent cyclic data structures and Marshal does not
// handle them.  Passing cyclic structures to Marshal will result in
// an infinite recursion.
//
func Marshal(v interface{}) ([]byte, os.Error) {
	e := &encodeState{}
	err := e.marshal(v)
	if err != nil {
		return nil, err
	}
	return e.Bytes(), nil
}

// MarshalIndent is like Marshal but applies Indent to format the output.
func MarshalIndent(v interface{}, prefix, indent string) ([]byte, os.Error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = Indent(&buf, b, prefix, indent)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalForHTML is like Marshal but applies HTMLEscape to the output.
func MarshalForHTML(v interface{}) ([]byte, os.Error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	HTMLEscape(&buf, b)
	return buf.Bytes(), nil
}

// HTMLEscape appends to dst the JSON-encoded src with <, >, and &
// characters inside string literals changed to \u003c, \u003e, \u0026
// so that the JSON will be safe to embed inside HTML <script> tags.
// For historical reasons, web browsers don't honor standard HTML
// escaping within <script> tags, so an alternative JSON encoding must
// be used.
func HTMLEscape(dst *bytes.Buffer, src []byte) {
	// < > & can only appear in string literals,
	// so just scan the string one byte at a time.
	start := 0
	for i, c := range src {
		if c == '<' || c == '>' || c == '&' {
			if start < i {
				dst.Write(src[start:i])
			}
			dst.WriteString(`\u00`)
			dst.WriteByte(hex[c>>4])
			dst.WriteByte(hex[c&0xF])
			start = i + 1
		}
	}
	if start < len(src) {
		dst.Write(src[start:])
	}
}

// Marshaler is the interface implemented by objects that
// can marshal themselves into valid JSON.
type Marshaler interface {
	MarshalJSON() ([]byte, os.Error)
}

type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) String() string {
	return "json: unsupported type: " + e.Type.String()
}

type InvalidUTF8Error struct {
	S string
}

func (e *InvalidUTF8Error) String() string {
	return "json: invalid UTF-8 in string: " + strconv.Quote(e.S)
}

type MarshalerError struct {
	Type  reflect.Type
	Error os.Error
}

func (e *MarshalerError) String() string {
	return "json: error calling MarshalJSON for type " + e.Type.String() + ": " + e.Error.String()
}

type interfaceOrPtrValue interface {
	IsNil() bool
	Elem() reflect.Value
}

var hex = "0123456789abcdef"

// An encodeState encodes JSON into a bytes.Buffer.
type encodeState struct {
	bytes.Buffer // accumulated output
}

func (e *encodeState) marshal(v interface{}) (err os.Error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(runtime.Error); ok {
				panic(r)
			}
			err = r.(os.Error)
		}
	}()
	e.reflectValue(reflect.ValueOf(v))
	return nil
}

func (e *encodeState) error(err os.Error) {
	panic(err)
}

var byteSliceType = reflect.TypeOf([]byte(nil))

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

func (e *encodeState) reflectValue(v reflect.Value) {
	e.reflectValueQuoted(v, false)
}

// reflectValueQuoted writes the value in v to the output.
// If quoted is true, the serialization is wrapped in a JSON string.
func (e *encodeState) reflectValueQuoted(v reflect.Value, quoted bool) {
	if !v.IsValid() {
		e.WriteString("null")
		return
	}

	if j, ok := v.Interface().(Marshaler); ok {
		b, err := j.MarshalJSON()
		if err == nil {
			// copy JSON into buffer, checking validity.
			err = Compact(&e.Buffer, b)
		}
		if err != nil {
			e.error(&MarshalerError{v.Type(), err})
		}
		return
	}

	writeString := (*encodeState).WriteString
	if quoted {
		writeString = (*encodeState).string
	}

	switch v.Kind() {
	case reflect.Bool:
		x := v.Bool()
		if x {
			writeString(e, "true")
		} else {
			writeString(e, "false")
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		writeString(e, strconv.Itoa64(v.Int()))

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		writeString(e, strconv.Uitoa64(v.Uint()))

	case reflect.Float32, reflect.Float64:
		writeString(e, strconv.FtoaN(v.Float(), 'g', -1, v.Type().Bits()))

	case reflect.String:
		if quoted {
			sb, err := Marshal(v.String())
			if err != nil {
				e.error(err)
			}
			e.string(string(sb))
		} else {
			e.string(v.String())
		}

	case reflect.Struct:
		e.WriteByte('{')
		t := v.Type()
		n := v.NumField()
		first := true
		for i := 0; i < n; i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			tag, omitEmpty, quoted := f.Name, false, false
			if tv := f.Tag.Get("json"); tv != "" {
				name, opts := parseTag(tv)
				if isValidTag(name) {
					tag = name
				}
				omitEmpty = opts.Contains("omitempty")
				quoted = opts.Contains("string")
			}
			fieldValue := v.Field(i)
			if omitEmpty && isEmptyValue(fieldValue) {
				continue
			}
			if first {
				first = false
			} else {
				e.WriteByte(',')
			}
			e.string(tag)
			e.WriteByte(':')
			e.reflectValueQuoted(fieldValue, quoted)
		}
		e.WriteByte('}')

	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			e.error(&UnsupportedTypeError{v.Type()})
		}
		if v.IsNil() {
			e.WriteString("null")
			break
		}
		e.WriteByte('{')
		var sv stringValues = v.MapKeys()
		sort.Sort(sv)
		for i, k := range sv {
			if i > 0 {
				e.WriteByte(',')
			}
			e.string(k.String())
			e.WriteByte(':')
			e.reflectValue(v.MapIndex(k))
		}
		e.WriteByte('}')

	case reflect.Array, reflect.Slice:
		if v.Type() == byteSliceType {
			e.WriteByte('"')
			s := v.Interface().([]byte)
			if len(s) < 1024 {
				// for small buffers, using Encode directly is much faster.
				dst := make([]byte, base64.StdEncoding.EncodedLen(len(s)))
				base64.StdEncoding.Encode(dst, s)
				e.Write(dst)
			} else {
				// for large buffers, avoid unnecessary extra temporary
				// buffer space.
				enc := base64.NewEncoder(base64.StdEncoding, e)
				enc.Write(s)
				enc.Close()
			}
			e.WriteByte('"')
			break
		}
		e.WriteByte('[')
		n := v.Len()
		for i := 0; i < n; i++ {
			if i > 0 {
				e.WriteByte(',')
			}
			e.reflectValue(v.Index(i))
		}
		e.WriteByte(']')

	case reflect.Interface, reflect.Ptr:
		if v.IsNil() {
			e.WriteString("null")
			return
		}
		e.reflectValue(v.Elem())

	default:
		e.error(&UnsupportedTypeError{v.Type()})
	}
	return
}

func isValidTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '$' && c != '-' && c != '_' && !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

// stringValues is a slice of reflect.Value holding *reflect.StringValue.
// It implements the methods to sort by string.
type stringValues []reflect.Value

func (sv stringValues) Len() int           { return len(sv) }
func (sv stringValues) Swap(i, j int)      { sv[i], sv[j] = sv[j], sv[i] }
func (sv stringValues) Less(i, j int) bool { return sv.get(i) < sv.get(j) }
func (sv stringValues) get(i int) string   { return sv[i].String() }

func (e *encodeState) string(s string) (int, os.Error) {
	len0 := e.Len()
	e.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if 0x20 <= b && b != '\\' && b != '"' && b != '<' && b != '>' {
				i++
				continue
			}
			if start < i {
				e.WriteString(s[start:i])
			}
			switch b {
			case '\\', '"':
				e.WriteByte('\\')
				e.WriteByte(b)
			case '\n':
				e.WriteByte('\\')
				e.WriteByte('n')
			case '\r':
				e.WriteByte('\\')
				e.WriteByte('r')
			default:
				// This encodes bytes < 0x20 except for \n and \r,
				// as well as < and >. The latter are escaped because they
				// can lead to security holes when user-controlled strings
				// are rendered into JSON and served to some browsers.
				e.WriteString(`\u00`)
				e.WriteByte(hex[b>>4])
				e.WriteByte(hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			e.error(&InvalidUTF8Error{s})
		}
		i += size
	}
	if start < len(s) {
		e.WriteString(s[start:])
	}
	e.WriteByte('"')
	return e.Len() - len0, nil
}
