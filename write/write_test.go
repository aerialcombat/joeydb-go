package write

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGoldenEncoding(t *testing.T) {
	absolute := time.Unix(0, 1_780_000_000_000_000_000).UTC()
	tests := []struct {
		name    string
		request Request
	}{
		{
			name: "heartbeat",
			request: Request{
				Records: []Record{{
					Subject: "worker:git-ingestion", Predicate: "obs:heartbeat",
					Object:     Entity("service:project-observatory"),
					Expiration: After(30 * time.Second),
				}},
				Vocabulary: CreateUnknown,
			},
		},
		{
			name: "task",
			request: Request{
				Records: []Record{
					{
						Subject: "task:1", Predicate: "obs:task_project",
						Object: Entity("project:1"), Mode: Ensure,
					},
					{
						Subject: "task:1", Predicate: "obs:title",
						Object: Entity("text:VGFzaw"), Mode: Ensure,
					},
					{
						Subject: "task:1", Predicate: "obs:status",
						Object: Entity("status:open"), Mode: Replace,
					},
				},
				Vocabulary: CreateUnknown,
			},
		},
		{
			name: "mutations",
			request: Request{
				Records: []Record{
					{
						Subject: "metric:1", Predicate: "obs:value", Object: Number(42),
						Tense: "tense:present", RawText: "forty-two", Expiration: At(absolute),
					},
					{
						Subject: "set:1", Predicate: "obs:member",
						Object: Entity("thing:1"), Mode: Ensure,
					},
					{
						Subject: "hash:1", Predicate: "obs:status",
						Object: Entity("status:open"), Mode: Replace,
					},
				},
				Retractions: []Retraction{
					RetractFact("10"),
					RetractExact("set:2", "obs:member", Entity("thing:2")),
					RetractExact("set:3", "obs:value", Number(7)),
					RetractSlot("hash:2", "obs:status"),
				},
				Corrections: []Correction{
					Correct("11", Record{
						Subject: "task:1", Predicate: "obs:status",
						Object: Entity("status:done"), Expiration: After(5 * time.Second),
					}),
				},
				Expirations: []Expiration{
					ExpireAfter("12", time.Minute),
					ExpireAt("13", absolute),
				},
				Persistence:     []Persistence{Persist("14")},
				Vocabulary:      CreateUnknown,
				TransactionTime: TransactionNanoseconds(-5),
			},
		},
		{
			name: "transaction-zero",
			request: Request{
				Retractions:     []Retraction{RetractFact("1")},
				TransactionTime: TransactionNanoseconds(0),
			},
		},
		{
			name: "transaction-time",
			request: Request{
				Retractions:     []Retraction{RetractFact("2")},
				TransactionTime: AtTransactionTime(absolute),
			},
		},
		{
			name: "reject",
			request: Request{
				Records: []Record{{
					Subject: "subject:known", Predicate: "predicate:known",
					Object: Entity("object:known"),
				}},
				Vocabulary: RejectUnknown,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.request.Encode()
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", test.name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			want = bytes.TrimSpace(want)
			if !bytes.Equal(got, want) {
				t.Fatalf("encoding mismatch\n got: %s\nwant: %s", got, want)
			}
			viaJSON, err := json.Marshal(test.request)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(viaJSON, got) {
				t.Fatalf("MarshalJSON differs\nencode: %s\nmarshal: %s", got, viaJSON)
			}
			again, err := test.request.Encode()
			if err != nil || !bytes.Equal(again, got) {
				t.Fatalf("encoding is not deterministic: %s / %s / %v", got, again, err)
			}
		})
	}
}

func TestValidationCodesAndPaths(t *testing.T) {
	base := func() Request {
		return Request{
			Records: []Record{{
				Subject: "subject:1", Predicate: "predicate:1",
				Object: Entity("object:1"),
			}},
			Vocabulary: CreateUnknown,
		}
	}
	tests := []struct {
		name string
		edit func(*Request)
		code Code
		path string
	}{
		{"empty request", func(request *Request) { *request = Request{} },
			CodeEmptyRequest, "request"},
		{"vocabulary required", func(request *Request) { request.Vocabulary = "" },
			CodeVocabularyRequired, "vocabulary"},
		{"vocabulary unsupported", func(request *Request) { request.Vocabulary = "invent" },
			CodeUnsupportedValue, "vocabulary"},
		{"vocabulary unnecessary", func(request *Request) {
			*request = Request{
				Retractions: []Retraction{RetractFact("1")},
				Vocabulary:  CreateUnknown,
			}
		}, CodeVocabularyUnnecessary, "vocabulary"},
		{"subject empty", func(request *Request) { request.Records[0].Subject = "" },
			CodeMissingField, "record[0].subject"},
		{"predicate empty", func(request *Request) { request.Records[0].Predicate = "" },
			CodeMissingField, "record[0].predicate"},
		{"object empty", func(request *Request) { request.Records[0].Object = Object{} },
			CodeInvalidObject, "record[0].object"},
		{"object number unsafe", func(request *Request) {
			request.Records[0].Object = Number(MaxJSSafeInteger + 1)
		}, CodeNumberOutOfRange, "record[0].object"},
		{"mode unsupported", func(request *Request) { request.Records[0].Mode = "upsert" },
			CodeUnsupportedValue, "record[0].mode"},
		{"ensure attributes", func(request *Request) {
			request.Records[0].Mode = Ensure
			request.Records[0].RawText = "text"
		}, CodeIncompatibleFields, "record[0].mode"},
		{"ttl zero", func(request *Request) {
			request.Records[0].Expiration = After(0)
		}, CodeInvalidDuration, "record[0].expiration"},
		{"ttl negative", func(request *Request) {
			request.Records[0].Expiration = After(-time.Millisecond)
		}, CodeInvalidDuration, "record[0].expiration"},
		{"ttl lossy", func(request *Request) {
			request.Records[0].Expiration = After(time.Millisecond + time.Nanosecond)
		}, CodeInvalidDuration, "record[0].expiration"},
		{"absolute nonpositive", func(request *Request) {
			request.Records[0].Expiration = At(time.Unix(0, 0))
		}, CodeInvalidTime, "record[0].expiration"},
		{"fact id noncanonical", func(request *Request) {
			*request = Request{Retractions: []Retraction{RetractFact("01")}}
		}, CodeInvalidFactID, "retract[0].fact"},
		{"retraction unset", func(request *Request) {
			*request = Request{Retractions: []Retraction{{}}}
		}, CodeMissingField, "retract[0]"},
		{"correction mode", func(request *Request) {
			request.Records = nil
			request.Corrections = []Correction{Correct("1", Record{
				Subject: "s", Predicate: "p", Object: Entity("o"), Mode: Ensure,
			})}
		}, CodeIncompatibleFields, "correct[0].with.mode"},
		{"expiration missing deadline", func(request *Request) {
			*request = Request{Expirations: []Expiration{{factID: "1"}}}
		}, CodeMissingField, "expire[0].expiration"},
		{"persistence id", func(request *Request) {
			*request = Request{Persistence: []Persistence{Persist("+1")}}
		}, CodeInvalidFactID, "persist[0].fact"},
		{"duplicate fact target", func(request *Request) {
			*request = Request{
				Retractions: []Retraction{RetractFact("7")},
				Persistence: []Persistence{Persist("7")},
			}
		}, CodeDuplicateTarget, "persist[0].fact"},
		{"duplicate exact target", func(request *Request) {
			request.Records = []Record{
				{Subject: "s", Predicate: "p", Object: Entity("o"), Mode: Ensure},
				{Subject: "s", Predicate: "p", Object: Entity("o"), Mode: Ensure},
			}
		}, CodeDuplicateTarget, "record[1]"},
		{"slot exact conflict", func(request *Request) {
			request.Records = []Record{
				{Subject: "s", Predicate: "p", Object: Entity("o"), Mode: Ensure},
				{Subject: "s", Predicate: "p", Object: Entity("x"), Mode: Replace},
			}
		}, CodeDestinationConflict, "record[1]"},
		{"append destination conflict", func(request *Request) {
			request.Records = []Record{
				{Subject: "s", Predicate: "p", Object: Entity("o"), Mode: Ensure},
				{Subject: "s", Predicate: "p", Object: Entity("o")},
			}
		}, CodeDestinationConflict, "record[1]"},
		{"correction destination conflict", func(request *Request) {
			request.Records = []Record{
				{Subject: "s", Predicate: "p", Object: Entity("o"), Mode: Ensure},
			}
			request.Corrections = []Correction{
				Correct("8", Record{Subject: "s", Predicate: "p", Object: Entity("o")}),
			}
		}, CodeDestinationConflict, "correct[0].with"},
		{"invalid utf8", func(request *Request) {
			request.Records[0].RawText = string([]byte{0xff})
		}, CodeInvalidUTF8, "record[0].raw_text"},
		{"tx time range", func(request *Request) {
			request.TransactionTime = AtTransactionTime(time.Date(
				3000, time.January, 1, 0, 0, 0, 0, time.UTC,
			))
		}, CodeInvalidTime, "tx_time"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base()
			test.edit(&request)
			err := request.Validate()
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error=%T %v, want ValidationError", err, err)
			}
			if validation.Code != test.code || validation.Path != test.path {
				t.Fatalf("validation=%+v, want code=%s path=%s",
					validation, test.code, test.path)
			}
			if _, encodeErr := request.Encode(); !reflect.DeepEqual(encodeErr, err) {
				var encoded *ValidationError
				if !errors.As(encodeErr, &encoded) ||
					encoded.Code != validation.Code || encoded.Path != validation.Path {
					t.Fatalf("Validate=%v Encode=%v", err, encodeErr)
				}
			}
			if !strings.Contains(err.Error(), "path="+test.path+" code="+string(test.code)) {
				t.Fatalf("nondiagnostic error: %v", err)
			}
		})
	}
}

func TestFeaturesAreMinimalAndDefensive(t *testing.T) {
	request := Request{
		Records: []Record{
			{Subject: "s", Predicate: "p", Object: Entity("o")},
			{Subject: "s2", Predicate: "p2", Object: Number(2), Mode: Replace},
		},
		Retractions: []Retraction{
			RetractFact("1"),
			RetractExact("x", "p", Entity("y")),
			RetractSlot("z", "p"),
		},
		Corrections: []Correction{
			Correct("2", Record{Subject: "c", Predicate: "p", Object: Entity("o")}),
		},
		Expirations: []Expiration{ExpireAfter("3", time.Second)},
		Persistence: []Persistence{Persist("4")},
		Vocabulary:  RejectUnknown,
	}
	got := request.Features()
	if !reflect.DeepEqual(got.Operations, []Operation{
		OperationCorrect, OperationExpire, OperationPersist, OperationRecord, OperationRetract,
	}) ||
		!reflect.DeepEqual(got.ObjectKinds, []ObjectKind{ObjectEntityLabel, ObjectU64}) ||
		!reflect.DeepEqual(got.ExpirationForms, []ExpirationForm{ExpirationRelative}) ||
		!reflect.DeepEqual(got.RecordModes, []RecordMode{Append, Replace}) ||
		!reflect.DeepEqual(got.RetractionSelectors,
			[]RetractionSelector{SelectorFact, SelectorSlot, SelectorWhere}) {
		t.Fatalf("features=%+v", got)
	}
	got.Operations[0] = "mutated"
	if request.Features().Operations[0] != OperationCorrect {
		t.Fatal("Features returned aliased slices")
	}
}

func TestTypedResponseDecode(t *testing.T) {
	var response Response
	err := json.Unmarshal([]byte(`{
		"committed":true,"watermark":9,"log_identity":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"facts":[{"id":"7","subject":"s","predicate":"p","object":"42"}],
		"retracted":["1"],"corrected":[{"superseded":"2","id":"8"}],
		"expirations":[{"fact":"7","expires_at_ns":"99","changed":true}],
		"created_entities":[{"id":"3","label":"s"}],
		"logical":[{"scope":"record","index":0,"operation":"ensure","outcome":"created","id":"7"}]
	}`), &response)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Committed || response.Watermark != 9 ||
		response.Facts[0].ID != "7" || response.Expirations[0].ExpiresAtNS != "99" ||
		response.Logical[0].Outcome != "created" {
		t.Fatalf("response=%+v", response)
	}
}

func TestPublicConstructorsAndAccessors(t *testing.T) {
	entity := Entity("entity:1")
	if entity.Kind() != ObjectEntityLabel {
		t.Fatalf("entity kind=%q", entity.Kind())
	}
	if label, ok := entity.EntityLabel(); !ok || label != "entity:1" {
		t.Fatalf("entity label=%q ok=%t", label, ok)
	}
	number := Number(42)
	if number.Kind() != ObjectU64 {
		t.Fatalf("number kind=%q", number.Kind())
	}
	if value, ok := number.Uint64(); !ok || value != 42 {
		t.Fatalf("number=%d ok=%t", value, ok)
	}
	correction := Correct("7", Record{
		Subject: "s", Predicate: "p", Object: entity,
	})
	if correction.Target() != "7" || correction.Replacement().Subject != "s" {
		t.Fatalf("correction target=%q replacement=%+v",
			correction.Target(), correction.Replacement())
	}
	if err := Validate(Request{
		Retractions: []Retraction{RetractFact("1")},
	}); err != nil {
		t.Fatal(err)
	}
	for _, object := range []Object{{}, Entity(""), Number(MaxJSSafeInteger + 1)} {
		if _, err := json.Marshal(object); err == nil {
			t.Fatalf("json.Marshal accepted invalid object %+v", object)
		}
	}
}

func FuzzFactIDValidation(f *testing.F) {
	for _, seed := range []string{"1", "0", "01", "+1", "-1", "18446744073709551615"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		request := Request{Retractions: []Retraction{RetractFact(value)}}
		_, _ = request.Encode()
	})
}

func FuzzDurationValidation(f *testing.F) {
	for _, seed := range []int64{-1, 0, 1, int64(time.Millisecond), int64(time.Second)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, nanoseconds int64) {
		request := Request{
			Records: []Record{{
				Subject: "s", Predicate: "p", Object: Entity("o"),
				Expiration: After(time.Duration(nanoseconds)),
			}},
			Vocabulary: CreateUnknown,
		}
		_, _ = request.Encode()
	})
}

func FuzzObjectDiscriminant(f *testing.F) {
	for _, seed := range []uint8{0, 1, 2, 3, 255} {
		f.Add(seed, uint64(0), "entity:x")
	}
	f.Fuzz(func(t *testing.T, tag uint8, number uint64, label string) {
		request := Request{
			Records: []Record{{
				Subject: "s", Predicate: "p",
				Object: Object{tag: objectTag(tag), number: number, label: label},
			}},
			Vocabulary: CreateUnknown,
		}
		first, firstErr := request.Encode()
		second, secondErr := request.Encode()
		if (firstErr == nil) != (secondErr == nil) || !bytes.Equal(first, second) {
			t.Fatalf("nondeterministic encoding: %q/%v then %q/%v",
				first, firstErr, second, secondErr)
		}
	})
}

func BenchmarkEncode(b *testing.B) {
	request := Request{
		Records: []Record{{
			Subject: "worker:git-ingestion", Predicate: "obs:heartbeat",
			Object:     Entity("service:project-observatory"),
			Expiration: After(30 * time.Second),
		}},
		Vocabulary: CreateUnknown,
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := request.Encode(); err != nil {
			b.Fatal(err)
		}
	}
}
