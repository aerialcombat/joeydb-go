package joeydb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestSemanticKeyGolden(t *testing.T) {
	var fixture struct {
		Domain string `json:"domain"`
		Cases  []struct {
			Namespace string   `json:"namespace"`
			Parts     []string `json:"parts"`
			Suffix    string   `json:"suffix"`
		} `json:"cases"`
	}
	body, err := os.ReadFile("testdata/semantic-keys.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Domain != SemanticKeyDomain {
		t.Fatalf("domain=%q", fixture.Domain)
	}

	seen := make(map[string]struct{}, len(fixture.Cases))
	for _, test := range fixture.Cases {
		key, err := SemanticKey(test.Namespace, test.Parts...)
		if err != nil {
			t.Fatalf("%s %q: %v", test.Namespace, test.Parts, err)
		}
		suffix, ok := key.Suffix()
		if !ok || suffix != test.Suffix {
			t.Fatalf("%s %q: suffix=%q ok=%t want=%q",
				test.Namespace, test.Parts, suffix, ok, test.Suffix)
		}
		if len(suffix) > maxSemanticKeyNamespaceBytes+1+43 {
			t.Fatalf("suffix is not fixed-bounded: %q", suffix)
		}
		if _, duplicate := seen[suffix]; duplicate {
			t.Fatalf("fixture collision: %q", suffix)
		}
		seen[suffix] = struct{}{}
	}
}

func TestSemanticKeyNamespaceValidationAndKeyForm(t *testing.T) {
	for _, namespace := range []string{
		"", "1task", "Task", "task status", "task:status", "task/status",
		strings.Repeat("a", maxSemanticKeyNamespaceBytes+1),
		string([]byte{'a', 0xff}),
	} {
		key, err := SemanticKey(namespace, "part")
		var invalid *InvalidKeyError
		if key != (WriteKey{}) || !errors.As(err, &invalid) ||
			invalid.Key != namespace {
			t.Fatalf("namespace=%q key=%+v err=%v", namespace, key, err)
		}
	}

	key, err := SemanticKey(strings.Repeat("a", maxSemanticKeyNamespaceBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := key.Suffix(); !ok {
		t.Fatal("semantic key is not a suffix")
	}
	if suffix, ok := FullKey(testPrefix + "complete").Suffix(); ok || suffix != "" {
		t.Fatalf("full key exposed as suffix: %q %t", suffix, ok)
	}
	if suffix, ok := (WriteKey{}).Suffix(); ok || suffix != "" {
		t.Fatalf("zero key exposed as suffix: %q %t", suffix, ok)
	}
}

func TestSemanticKeyUsesSessionPrefixValidation(t *testing.T) {
	key, err := SemanticKey("task-status", "task:1", "queued")
	if err != nil {
		t.Fatal(err)
	}
	var capabilities Capabilities
	capabilities.Write.Idempotency.RequiredKeyPrefix = testPrefix
	capabilities.Write.Idempotency.MaxKeyBytes = 128
	session := &Session{capabilities: capabilities}
	resolved, err := session.resolveWriteKey(key)
	if err != nil {
		t.Fatal(err)
	}
	suffix, _ := key.Suffix()
	if resolved != testPrefix+suffix {
		t.Fatalf("resolved=%q", resolved)
	}

	capabilities.Write.Idempotency.MaxKeyBytes = len(testPrefix) + len(suffix) - 1
	session.capabilities = capabilities
	_, err = session.resolveWriteKey(key)
	var invalid *InvalidKeyError
	if !errors.As(err, &invalid) {
		t.Fatalf("err=%v", err)
	}
}

func FuzzSemanticKeyDeterminism(f *testing.F) {
	f.Add("task-status", "task:1", "queued")
	f.Add("boundary", "ab", "c")
	f.Add("empty", "", "")
	f.Fuzz(func(t *testing.T, namespace, first, second string) {
		left, leftErr := SemanticKey(namespace, first, second)
		right, rightErr := SemanticKey(namespace, first, second)
		if (leftErr == nil) != (rightErr == nil) {
			t.Fatal("validation is nondeterministic")
		}
		if leftErr != nil {
			return
		}
		leftSuffix, leftOK := left.Suffix()
		rightSuffix, rightOK := right.Suffix()
		if !leftOK || !rightOK || leftSuffix != rightSuffix {
			t.Fatalf("derivation is nondeterministic: %q %q", leftSuffix, rightSuffix)
		}
		if !strings.HasPrefix(leftSuffix, namespace+":") ||
			len(leftSuffix) != len(namespace)+1+43 {
			t.Fatalf("unexpected suffix form %q", leftSuffix)
		}
	})
}

func ExampleSemanticKey() {
	key, err := SemanticKey("task-status", "task:1", "queued", "fact:7")
	if err != nil {
		fmt.Println(err)
		return
	}
	suffix, _ := key.Suffix()
	fmt.Println(strings.HasPrefix(suffix, "task-status:"), len(suffix))
	// Output: true 55
}
