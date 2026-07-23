package write

import "time"

// EncodingDomain permanently identifies the exact JSON byte mapping used by
// Request.Encode. It was first published in joeydb-go v0.2.0.
//
// The value is metadata only: it is not added to request JSON or idempotency
// keys. Existing Request semantics must retain identical encoded bytes within
// this domain. An incompatible encoder requires a new, explicitly selected
// domain and migration plan.
const EncodingDomain = "github.com/aerialcombat/joeydb-go/write/v1"

// MaxJSSafeInteger is JoeyDB's largest public JSON numeric-object value.
const MaxJSSafeInteger = uint64(1)<<53 - 1

// RecordMode selects JoeyDB collection semantics for one record.
type RecordMode string

const (
	// Append is the canonical typed representation of append semantics. It is
	// intentionally the zero value and is omitted from JSON.
	Append  RecordMode = ""
	Ensure  RecordMode = "ensure"
	Replace RecordMode = "replace"
)

// CapabilityName returns the advertised JoeyDB record-mode name.
func (mode RecordMode) CapabilityName() string {
	if mode == Append {
		return "append"
	}
	return string(mode)
}

// VocabularyMode controls resolution of unknown entity labels.
type VocabularyMode string

const (
	CreateUnknown VocabularyMode = "create"
	RejectUnknown VocabularyMode = "reject"
)

// ObjectKind describes a typed JoeyDB write object.
type ObjectKind string

const (
	ObjectEntityLabel ObjectKind = "entity_label"
	ObjectU64         ObjectKind = "u64"
)

type objectTag uint8

const (
	objectUnset objectTag = iota
	objectEntity
	objectNumber
)

// Object is exactly one entity label or JavaScript-safe u64 number.
type Object struct {
	tag    objectTag
	label  string
	number uint64
}

// Entity constructs an entity-label object.
func Entity(label string) Object {
	return Object{tag: objectEntity, label: label}
}

// Number constructs a u64 numeric object. Validate rejects values above
// MaxJSSafeInteger.
func Number(value uint64) Object {
	return Object{tag: objectNumber, number: value}
}

// Kind reports the object variant. A zero Object reports "".
func (o Object) Kind() ObjectKind {
	switch o.tag {
	case objectEntity:
		return ObjectEntityLabel
	case objectNumber:
		return ObjectU64
	default:
		return ""
	}
}

// EntityLabel returns the entity label and whether this is an entity object.
func (o Object) EntityLabel() (string, bool) {
	return o.label, o.tag == objectEntity
}

// Uint64 returns the number and whether this is a numeric object.
func (o Object) Uint64() (uint64, bool) {
	return o.number, o.tag == objectNumber
}

type deadlineTag uint8

const (
	deadlineUnset deadlineTag = iota
	deadlineAfter
	deadlineAt
)

// Deadline is an optional relative or absolute fact expiration.
type Deadline struct {
	tag      deadlineTag
	duration time.Duration
	at       time.Time
}

// After constructs a relative expiration. Validation requires a positive
// duration exactly representable as whole milliseconds.
func After(duration time.Duration) Deadline {
	return Deadline{tag: deadlineAfter, duration: duration}
}

// At constructs an absolute expiration encoded as Unix nanoseconds.
func At(deadline time.Time) Deadline {
	return Deadline{tag: deadlineAt, at: deadline}
}

// Record is one fact entry.
type Record struct {
	Subject    string
	Predicate  string
	Object     Object
	Tense      string
	RawText    string
	Mode       RecordMode
	Expiration Deadline
}

type retractionTag uint8

const (
	retractionUnset retractionTag = iota
	retractionFact
	retractionExact
	retractionSlot
)

// Retraction is exactly one fact-ID, exact-triple, or slot selector.
type Retraction struct {
	tag       retractionTag
	factID    string
	subject   string
	predicate string
	object    Object
}

// RetractFact retracts one active fact by canonical decimal ID.
func RetractFact(factID string) Retraction {
	return Retraction{tag: retractionFact, factID: factID}
}

// RetractExact removes an exact subject/predicate/object member.
func RetractExact(subject, predicate string, object Object) Retraction {
	return Retraction{
		tag: retractionExact, subject: subject, predicate: predicate, object: object,
	}
}

// RetractSlot removes a single-valued subject/predicate slot.
func RetractSlot(subject, predicate string) Retraction {
	return Retraction{tag: retractionSlot, subject: subject, predicate: predicate}
}

// Correction supersedes one active fact with a replacement.
type Correction struct {
	factID      string
	replacement Record
}

// Correct constructs a fact-ID correction.
func Correct(factID string, replacement Record) Correction {
	return Correction{factID: factID, replacement: replacement}
}

// Target returns the correction's canonical fact-ID input.
func (c Correction) Target() string { return c.factID }

// Replacement returns a copy of the correction replacement.
func (c Correction) Replacement() Record { return c.replacement }

// Expiration changes one active fact's deadline.
type Expiration struct {
	factID   string
	deadline Deadline
}

// ExpireAfter sets or replaces a relative fact deadline.
func ExpireAfter(factID string, duration time.Duration) Expiration {
	return Expiration{factID: factID, deadline: After(duration)}
}

// ExpireAt sets or replaces an absolute fact deadline.
func ExpireAt(factID string, deadline time.Time) Expiration {
	return Expiration{factID: factID, deadline: At(deadline)}
}

// Persistence removes one active fact's expiration.
type Persistence struct {
	factID string
}

// Persist constructs a persistence mutation.
func Persist(factID string) Persistence {
	return Persistence{factID: factID}
}

type transactionTimeTag uint8

const (
	transactionTimeUnset transactionTimeTag = iota
	transactionTimeAt
	transactionTimeNanoseconds
)

// TransactionTime is an optional, presence-preserving event timestamp.
type TransactionTime struct {
	tag         transactionTimeTag
	at          time.Time
	nanoseconds int64
}

// AtTransactionTime encodes a time.Time as exact Unix nanoseconds.
func AtTransactionTime(value time.Time) TransactionTime {
	return TransactionTime{tag: transactionTimeAt, at: value}
}

// TransactionNanoseconds pins an exact signed Unix-nanosecond value. Zero and
// negative values are valid and remain explicitly present.
func TransactionNanoseconds(value int64) TransactionTime {
	return TransactionTime{tag: transactionTimeNanoseconds, nanoseconds: value}
}

// Request is the typed JoeyDB facts-write request. The wire discriminator is
// generated by Encode and is not caller-controlled.
type Request struct {
	Records         []Record
	Retractions     []Retraction
	Corrections     []Correction
	Expirations     []Expiration
	Persistence     []Persistence
	Vocabulary      VocabularyMode
	TransactionTime TransactionTime
}

// Operation is one advertised JoeyDB write operation.
type Operation string

const (
	OperationCorrect Operation = "correct"
	OperationExpire  Operation = "expire"
	OperationPersist Operation = "persist"
	OperationRecord  Operation = "record"
	OperationRetract Operation = "retract"
)

// ExpirationForm is one advertised deadline representation.
type ExpirationForm string

const (
	ExpirationAbsolute ExpirationForm = "expires_at_ns"
	ExpirationRelative ExpirationForm = "ttl_ms"
)

// RetractionSelector is one advertised retraction variant.
type RetractionSelector string

const (
	SelectorFact  RetractionSelector = "fact"
	SelectorSlot  RetractionSelector = "slot"
	SelectorWhere RetractionSelector = "where"
)

// Features is the minimal advertised write vocabulary needed by a request.
// Each call to Request.Features returns fresh slices.
type Features struct {
	Operations          []Operation
	ObjectKinds         []ObjectKind
	ExpirationForms     []ExpirationForm
	VocabularyModes     []VocabularyMode
	RecordModes         []RecordMode
	RetractionSelectors []RetractionSelector
}
