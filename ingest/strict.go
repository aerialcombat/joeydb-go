package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

func decodeStrict(data []byte, target any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty body")
	}
	if !utf8.Valid(data) {
		return errors.New("input is not valid UTF-8")
	}
	if err := rejectUnpairedJSONSurrogates(data); err != nil {
		return err
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing content after object")
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return err
	}
	if containsNull(generic) {
		return errors.New("explicit null values are not allowed")
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > MaxJSONDepth {
			return fmt.Errorf("JSON nesting exceeds %d levels", MaxJSONDepth)
		}
		token, err := decoder.Token()
		if err != nil {
			return err
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
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				if seen[key] {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected delimiter %q", delim)
		}
	}
	return walk(1)
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
					return errors.New("JSON string contains an unpaired high surrogate")
				}
				low, ok := parseJSONHex4(data[i+3 : i+7])
				if !ok || low < 0xdc00 || low > 0xdfff {
					return errors.New("JSON string contains an unpaired high surrogate")
				}
				i += 6
			case value >= 0xdc00 && value <= 0xdfff:
				return errors.New("JSON string contains an unpaired low surrogate")
			}
		}
	}
	return nil
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

func containsNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, item := range typed {
			if containsNull(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsNull(item) {
				return true
			}
		}
	}
	return false
}
