package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenEncoding(t *testing.T) {
	identity := strings.Repeat("a", 32)
	tests := []struct {
		name    string
		request Request
	}{
		{
			name: "table",
			request: Request{
				Where:  Where{Predicate: Labels("obs:status")},
				Return: Table(IncludeFacts),
			},
		},
		{
			name: "graph",
			request: Request{
				Where: Where{
					Predicate: Labels("obs:belongs_to_project"),
					Object:    Labels("project:1", "project:2"),
				},
				Return: Graph(ExcludeFacts),
				Limit:  MaxResults(200),
			},
		},
		{
			name: "numeric",
			request: Request{
				Where: Where{
					Subject: Labels("sensor:1"),
					ObjectNumber: &NumericRange{
						GTE: Bound(20),
						LT:  Bound(10),
					},
				},
				Return:         Columnar(),
				Consistency:    AllowStale,
				Optimization:   Force(ColumnarView),
				ReadConstraint: ReadAfter(7, identity),
				Limit:          MaxResults(25),
				Order: []Order{{
					By: ByObjectNumber, Direction: Descending,
				}},
				Offset: 2,
			},
		},
		{
			name: "escaped-all",
			request: Request{
				Where:          Where{Subject: Labels("?literal")},
				Return:         Document(),
				Consistency:    Fresh,
				Optimization:   Force(PrimitiveScan),
				ReadConstraint: OnLog(identity),
			},
		},
		{
			name: "match-all",
			request: Request{
				Where:  All(),
				Return: KV(),
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
			Where:  Where{Predicate: Labels("p")},
			Return: Table(),
		}
	}
	tests := []struct {
		name string
		edit func(*Request)
		code Code
		path string
	}{
		{"zero where", func(request *Request) { request.Where = Where{} },
			CodeUnsafeMatchAll, "where"},
		{"all with filter", func(request *Request) {
			request.Where = All()
			request.Where.Subject = Labels("s")
		}, CodeIncompatibleFields, "where"},
		{"empty membership", func(request *Request) {
			request.Where = Where{Subject: Labels()}
		}, CodeMissingField, "where.subject"},
		{"empty label", func(request *Request) {
			request.Where = Where{Subject: Labels("")}
		}, CodeMissingField, "where.subject"},
		{"duplicate label", func(request *Request) {
			request.Where = Where{Subject: Labels("s", "s")}
		}, CodeDuplicateValue, "where.subject[1]"},
		{"invalid utf8", func(request *Request) {
			request.Where = Where{Subject: Labels(string([]byte{0xff}))}
		}, CodeInvalidUTF8, "where.subject"},
		{"entity and numeric object", func(request *Request) {
			request.Where = Where{
				Object: Labels("o"), ObjectNumber: &NumericRange{},
			}
		}, CodeIncompatibleFields, "where.object_number"},
		{"lower bounds conflict", func(request *Request) {
			request.Where = Where{ObjectNumber: &NumericRange{
				GT: Bound(1), GTE: Bound(2),
			}}
		}, CodeIncompatibleFields, "where.object_number"},
		{"upper bounds conflict", func(request *Request) {
			request.Where = Where{ObjectNumber: &NumericRange{
				LT: Bound(1), LTE: Bound(2),
			}}
		}, CodeIncompatibleFields, "where.object_number"},
		{"numeric graph", func(request *Request) {
			request.Where = Where{ObjectNumber: &NumericRange{}}
			request.Return = Graph()
		}, CodeIncompatibleFields, "where.object_number"},
		{"return missing", func(request *Request) { request.Return = Return{} },
			CodeMissingField, "return"},
		{"return options duplicate", func(request *Request) {
			request.Return = Table(IncludeFacts, ExcludeFacts)
		}, CodeIncompatibleFields, "return.include_facts"},
		{"return option invalid", func(request *Request) {
			request.Return = Table(FactInclusion(99))
		}, CodeUnsupportedValue, "return.include_facts"},
		{"consistency", func(request *Request) { request.Consistency = "eventual" },
			CodeUnsupportedValue, "consistency"},
		{"force representation", func(request *Request) {
			request.Optimization = Force("btree")
		}, CodeUnsupportedValue, "optimize.representation"},
		{"watermark zero", func(request *Request) {
			request.ReadConstraint = ReadAfter(0, strings.Repeat("a", 32))
		}, CodeMissingField, "required_watermark"},
		{"identity invalid", func(request *Request) {
			request.ReadConstraint = OnLog("ABC")
		}, CodeInvalidIdentity, "log_identity"},
		{"limit zero", func(request *Request) { request.Limit = MaxResults(0) },
			CodeInvalidLimit, "limit"},
		{"offset negative", func(request *Request) { request.Offset = -1 },
			CodeInvalidOffset, "offset"},
		{"graph order", func(request *Request) {
			request.Return = Graph()
			request.Order = []Order{{By: ByID}}
		}, CodeIncompatibleFields, "order"},
		{"graph offset", func(request *Request) {
			request.Return = Graph()
			request.Offset = 1
		}, CodeIncompatibleFields, "order"},
		{"order direction", func(request *Request) {
			request.Order = []Order{{By: ByID, Direction: "sideways"}}
		}, CodeInvalidOrder, "order[0].direction"},
		{"order duplicate", func(request *Request) {
			request.Order = []Order{{By: ByID}, {By: ByID}}
		}, CodeDuplicateValue, "order[1].by"},
		{"numeric order without range", func(request *Request) {
			request.Order = []Order{{By: ByObjectNumber}}
		}, CodeIncompatibleFields, "order[0].by"},
		{"numeric multi order", func(request *Request) {
			request.Where = Where{ObjectNumber: &NumericRange{}}
			request.Order = []Order{{By: ByObjectNumber}, {By: ByID}}
		}, CodeIncompatibleFields, "order"},
		{"unknown order", func(request *Request) {
			request.Order = []Order{{By: "count"}}
		}, CodeInvalidOrder, "order[0].by"},
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
			if _, encodeErr := request.Encode(); encodeErr == nil {
				t.Fatal("Encode accepted invalid request")
			} else {
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

func TestContradictoryNumericRangeIsValid(t *testing.T) {
	request := Request{
		Where: Where{ObjectNumber: &NumericRange{
			GTE: Bound(20),
			LT:  Bound(10),
		}},
		Return: Table(),
	}
	body, err := request.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"gte":"20","lt":"10"`)) {
		t.Fatalf("body=%s", body)
	}
}

func TestLabelsAndAccessorsAreDefensive(t *testing.T) {
	input := []string{"a", "b"}
	labels := Labels(input...)
	input[0] = "mutated"
	values, present := labels.Values()
	if !present || values[0] != "a" {
		t.Fatalf("values=%q present=%t", values, present)
	}
	values[0] = "again"
	next, _ := labels.Values()
	if next[0] != "a" {
		t.Fatal("Values returned an aliased slice")
	}
}

func TestPublicConstructorsAndAccessors(t *testing.T) {
	bound := Bound(0)
	if value, present := bound.Value(); !present || value != 0 {
		t.Fatalf("bound=%d present=%t", value, present)
	}
	result := Graph(ExcludeFacts)
	if result.Shape() != ShapeGraph || result.Inclusion() != ExcludeFacts {
		t.Fatalf("return shape=%q inclusion=%d", result.Shape(), result.Inclusion())
	}
	if representation, forced := Automatic().ForcedRepresentation(); forced ||
		representation != "" {
		t.Fatalf("automatic representation=%q forced=%t", representation, forced)
	}
	constraint := ReadAfter(7, strings.Repeat("a", 32))
	if watermark, identity, present := constraint.Values(); !present ||
		watermark != 7 || identity != strings.Repeat("a", 32) {
		t.Fatalf("constraint=%d/%q present=%t", watermark, identity, present)
	}
	limit := MaxResults(3)
	if value, present := limit.Value(); !present || value != 3 {
		t.Fatalf("limit=%d present=%t", value, present)
	}
	if err := Validate(Request{
		Where:  Where{Subject: Labels("s")},
		Return: Table(),
	}); err != nil {
		t.Fatal(err)
	}
}

func FuzzLabelEncoding(f *testing.F) {
	for _, seed := range []string{"label:x", "?literal", "", "한글", string([]byte{0xff})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, label string) {
		request := Request{
			Where:  Where{Subject: Labels(label)},
			Return: Table(),
		}
		first, firstErr := request.Encode()
		second, secondErr := request.Encode()
		if (firstErr == nil) != (secondErr == nil) || !bytes.Equal(first, second) {
			t.Fatalf("nondeterministic: %q/%v then %q/%v",
				first, firstErr, second, secondErr)
		}
	})
}

func FuzzValidationEncodingDeterminism(f *testing.F) {
	f.Add("s", "p", "o", uint8(0), 10)
	f.Add("?s", "", "o", uint8(2), 0)
	f.Fuzz(func(t *testing.T, subject, predicate, object string, inclusion uint8, limit int) {
		request := Request{
			Where: Where{
				Subject: Labels(subject), Predicate: Labels(predicate), Object: Labels(object),
			},
			Return: Table(FactInclusion(inclusion)),
			Limit:  MaxResults(limit),
		}
		first, firstErr := request.Encode()
		second, secondErr := request.Encode()
		if (firstErr == nil) != (secondErr == nil) || !bytes.Equal(first, second) {
			t.Fatalf("nondeterministic: %q/%v then %q/%v",
				first, firstErr, second, secondErr)
		}
	})
}

func BenchmarkEncode(b *testing.B) {
	request := Request{
		Where: Where{
			Predicate: Labels("obs:belongs_to_project"),
			Object:    Labels("project:1", "project:2"),
		},
		Return: Graph(ExcludeFacts),
		Limit:  MaxResults(200),
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := request.Encode(); err != nil {
			b.Fatal(err)
		}
	}
}
