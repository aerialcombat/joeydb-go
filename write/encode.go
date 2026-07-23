package write

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type decimalUint64 uint64

func (value decimalUint64) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(value), 10))
}

type decimalInt64 int64

func (value decimalInt64) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(int64(value), 10))
}

// MarshalJSON emits an Object in JoeyDB's entity-string or numeric-literal
// wire form.
func (object Object) MarshalJSON() ([]byte, error) {
	if err := validateObject(object, "object"); err != nil {
		return nil, err
	}
	return wireObject(object).MarshalJSON()
}

// wireObject is used only after Request.Validate. Keeping its marshaler
// separate avoids validating every object a second time during encoding while
// retaining safe standalone Object.MarshalJSON behavior.
type wireObject Object

func (object wireObject) MarshalJSON() ([]byte, error) {
	switch object.tag {
	case objectEntity:
		return json.Marshal(object.label)
	case objectNumber:
		return []byte(strconv.FormatUint(object.number, 10)), nil
	default:
		return nil, fmt.Errorf("write: object is unset")
	}
}

type wireRequest struct {
	Write      string            `json:"write"`
	Record     []wireRecord      `json:"record,omitempty"`
	Retract    []wireRetraction  `json:"retract,omitempty"`
	Correct    []wireCorrection  `json:"correct,omitempty"`
	Expire     []wireExpiration  `json:"expire,omitempty"`
	Persist    []wirePersistence `json:"persist,omitempty"`
	Vocabulary *wireVocabulary   `json:"vocabulary,omitempty"`
	TxTimeNS   *decimalInt64     `json:"tx_time_ns,omitempty"`
}

type wireRecord struct {
	Subject     string         `json:"subject"`
	Predicate   string         `json:"predicate"`
	Object      wireObject     `json:"object"`
	Tense       string         `json:"tense,omitempty"`
	RawText     string         `json:"raw_text,omitempty"`
	Mode        RecordMode     `json:"mode,omitempty"`
	TTLMS       *decimalUint64 `json:"ttl_ms,omitempty"`
	ExpiresAtNS *decimalInt64  `json:"expires_at_ns,omitempty"`
}

type wireRetraction struct {
	Fact  string     `json:"fact,omitempty"`
	Where *wireExact `json:"where,omitempty"`
	Slot  *wireSlot  `json:"slot,omitempty"`
}

type wireExact struct {
	Subject   string     `json:"subject"`
	Predicate string     `json:"predicate"`
	Object    wireObject `json:"object"`
}

type wireSlot struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
}

type wireCorrection struct {
	Supersede string     `json:"supersede"`
	With      wireRecord `json:"with"`
}

type wireExpiration struct {
	Fact        string         `json:"fact"`
	TTLMS       *decimalUint64 `json:"ttl_ms,omitempty"`
	ExpiresAtNS *decimalInt64  `json:"expires_at_ns,omitempty"`
}

type wirePersistence struct {
	Fact string `json:"fact"`
}

type wireVocabulary struct {
	OnUnknown VocabularyMode `json:"on_unknown"`
}

// Encode validates and returns one deterministic compact JSON request.
func (request Request) Encode() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	wire := wireRequest{Write: "facts"}
	wire.Record = make([]wireRecord, 0, len(request.Records))
	wire.Retract = make([]wireRetraction, 0, len(request.Retractions))
	wire.Correct = make([]wireCorrection, 0, len(request.Corrections))
	wire.Expire = make([]wireExpiration, 0, len(request.Expirations))
	wire.Persist = make([]wirePersistence, 0, len(request.Persistence))
	if request.Vocabulary != "" {
		wire.Vocabulary = &wireVocabulary{OnUnknown: request.Vocabulary}
	}
	for _, record := range request.Records {
		wire.Record = append(wire.Record, encodeRecord(record))
	}
	for _, retraction := range request.Retractions {
		item := wireRetraction{}
		switch retraction.tag {
		case retractionFact:
			item.Fact = retraction.factID
		case retractionExact:
			item.Where = &wireExact{
				Subject: retraction.subject, Predicate: retraction.predicate,
				Object: wireObject(retraction.object),
			}
		case retractionSlot:
			item.Slot = &wireSlot{
				Subject: retraction.subject, Predicate: retraction.predicate,
			}
		}
		wire.Retract = append(wire.Retract, item)
	}
	for _, correction := range request.Corrections {
		wire.Correct = append(wire.Correct, wireCorrection{
			Supersede: correction.factID,
			With:      encodeRecord(correction.replacement),
		})
	}
	for _, expiration := range request.Expirations {
		item := wireExpiration{Fact: expiration.factID}
		encodeDeadline(expiration.deadline, &item.TTLMS, &item.ExpiresAtNS)
		wire.Expire = append(wire.Expire, item)
	}
	for _, persistence := range request.Persistence {
		wire.Persist = append(wire.Persist, wirePersistence{Fact: persistence.factID})
	}
	switch request.TransactionTime.tag {
	case transactionTimeAt:
		value, _ := exactUnixNano(request.TransactionTime.at)
		decimal := decimalInt64(value)
		wire.TxTimeNS = &decimal
	case transactionTimeNanoseconds:
		decimal := decimalInt64(request.TransactionTime.nanoseconds)
		wire.TxTimeNS = &decimal
	}
	return json.Marshal(wire)
}

// MarshalJSON validates and uses the same deterministic encoder as Encode.
func (request Request) MarshalJSON() ([]byte, error) {
	return request.Encode()
}

func encodeRecord(record Record) wireRecord {
	wire := wireRecord{
		Subject: record.Subject, Predicate: record.Predicate,
		Object: wireObject(record.Object),
		Tense:  record.Tense, RawText: record.RawText, Mode: record.Mode,
	}
	encodeDeadline(record.Expiration, &wire.TTLMS, &wire.ExpiresAtNS)
	return wire
}

func encodeDeadline(
	deadline Deadline,
	ttl **decimalUint64,
	expiresAt **decimalInt64,
) {
	switch deadline.tag {
	case deadlineAfter:
		value := decimalUint64(deadline.duration / 1_000_000)
		*ttl = &value
	case deadlineAt:
		nanoseconds, _ := exactUnixNano(deadline.at)
		value := decimalInt64(nanoseconds)
		*expiresAt = &value
	}
}
