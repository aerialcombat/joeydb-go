package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

func decodeStrict(data []byte, target any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return invalid(CodeInvalidJSON, "input", "body must not be empty")
	}
	if !utf8.Valid(data) {
		return invalid(CodeInvalidUTF8, "input", "input must contain valid UTF-8")
	}
	if err := rejectUnpairedJSONSurrogates(data); err != nil {
		return err
	}
	nullPath, err := inspectJSON(data)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &typeError) {
			path := typeError.Field
			if path == "" {
				path = "input"
			}
			return invalidCause(CodeInvalidJSON, path, err.Error(), err)
		}
		const unknownPrefix = `json: unknown field "`
		if strings.HasPrefix(err.Error(), unknownPrefix) &&
			strings.HasSuffix(err.Error(), `"`) {
			field := strings.TrimSuffix(
				strings.TrimPrefix(err.Error(), unknownPrefix), `"`,
			)
			return invalidCause(CodeUnknownField, field,
				fmt.Sprintf("unknown field %q is not part of joeydb.ingestion/v1", field),
				err)
		}
		return invalidCause(CodeInvalidJSON, "input", err.Error(), err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invalidCause(CodeTrailingContent, "input",
			"trailing content after object", err)
	}
	if nullPath != "" {
		return invalid(CodeExplicitNull, nullPath, "explicit null is not allowed")
	}
	return nil
}

func inspectJSON(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nullPath := ""
	var walk func(int, string) error
	walk = func(depth int, path string) error {
		if depth > MaxJSONDepth {
			return invalid(CodeMaxDepth, jsonPath(path),
				fmt.Sprintf("JSON nesting exceeds %d levels", MaxJSONDepth))
		}
		token, err := decoder.Token()
		if err != nil {
			return invalidCause(CodeInvalidJSON, jsonPath(path), err.Error(), err)
		}
		if token == nil {
			if nullPath == "" {
				nullPath = jsonPath(path)
			}
			return nil
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return invalidCause(
						CodeInvalidJSON, jsonPath(path), err.Error(), err,
					)
				}
				key, ok := keyToken.(string)
				if !ok {
					return invalid(CodeInvalidJSON, jsonPath(path),
						"object key is not a string")
				}
				childPath := appendJSONField(path, key)
				if seen[key] {
					return invalid(CodeDuplicateField, childPath,
						fmt.Sprintf("duplicate field %q", key))
				}
				seen[key] = true
				if err := walk(depth+1, childPath); err != nil {
					return err
				}
			}
			if _, err = decoder.Token(); err != nil {
				return invalidCause(
					CodeInvalidJSON, jsonPath(path), err.Error(), err,
				)
			}
			return nil
		case '[':
			index := 0
			for decoder.More() {
				childPath := path + "[" + strconv.Itoa(index) + "]"
				if err := walk(depth+1, childPath); err != nil {
					return err
				}
				index++
			}
			if _, err = decoder.Token(); err != nil {
				return invalidCause(
					CodeInvalidJSON, jsonPath(path), err.Error(), err,
				)
			}
			return nil
		default:
			return invalid(CodeInvalidJSON, jsonPath(path),
				fmt.Sprintf("unexpected delimiter %q", delim))
		}
	}
	err := walk(1, "")
	return nullPath, err
}

func rejectUnpairedJSONSurrogates(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || i+1 >= len(data) {
				continue
			}
			i++
			if data[i] != 'u' || i+4 >= len(data) {
				continue
			}
			value, ok := parseJSONHex4(data[i+1 : i+5])
			if !ok {
				continue
			}
			i += 4
			switch {
			case value >= 0xd800 && value <= 0xdbff:
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return invalid(CodeInvalidUnicode, "input",
						"JSON string contains an unpaired high surrogate")
				}
				low, ok := parseJSONHex4(data[i+3 : i+7])
				if !ok || low < 0xdc00 || low > 0xdfff {
					return invalid(CodeInvalidUnicode, "input",
						"JSON string contains an unpaired high surrogate")
				}
				i += 6
			case value >= 0xdc00 && value <= 0xdfff:
				return invalid(CodeInvalidUnicode, "input",
					"JSON string contains an unpaired low surrogate")
			}
		}
	}
	return nil
}

func appendJSONField(path, field string) string {
	if path == "" {
		return field
	}
	return path + "." + field
}

func jsonPath(path string) string {
	if path == "" {
		return "input"
	}
	return path
}

func parseJSONHex4(data []byte) (uint16, bool) {
	if len(data) != 4 {
		return 0, false
	}
	var value uint16
	for _, char := range data {
		value <<= 4
		switch {
		case char >= '0' && char <= '9':
			value |= uint16(char - '0')
		case char >= 'a' && char <= 'f':
			value |= uint16(char-'a') + 10
		case char >= 'A' && char <= 'F':
			value |= uint16(char-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
