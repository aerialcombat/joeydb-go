package write

import (
	"math"
	"strconv"
	"time"
	"unicode/utf8"
)

// Validate checks every state-independent JoeyDB facts-write rule.
func Validate(request Request) error {
	return request.Validate()
}

// Validate checks every state-independent JoeyDB facts-write rule.
func (request Request) Validate() error {
	if len(request.Records) == 0 &&
		len(request.Retractions) == 0 &&
		len(request.Corrections) == 0 &&
		len(request.Expirations) == 0 &&
		len(request.Persistence) == 0 {
		return invalid(CodeEmptyRequest, "request",
			"a write requires record, retract, correct, expire, or persist")
	}

	createsEntities := len(request.Records) > 0 || len(request.Corrections) > 0
	switch {
	case createsEntities && request.Vocabulary == "":
		return invalid(CodeVocabularyRequired, "vocabulary",
			"vocabulary is required when record or correct entries are present")
	case !createsEntities && request.Vocabulary != "":
		return invalid(CodeVocabularyUnnecessary, "vocabulary",
			"vocabulary is not used without record or correct entries")
	case request.Vocabulary != "" &&
		request.Vocabulary != CreateUnknown &&
		request.Vocabulary != RejectUnknown:
		return invalid(CodeUnsupportedValue, "vocabulary",
			"must be create or reject")
	}

	for i, record := range request.Records {
		if err := validateRecord(record, pathIndex("record", i)); err != nil {
			return err
		}
	}

	factTargets := make(map[string]string)
	for i, retraction := range request.Retractions {
		path := pathIndex("retract", i)
		switch retraction.tag {
		case retractionFact:
			if err := validateFactID(retraction.factID, path+".fact"); err != nil {
				return err
			}
			if err := claimFactTarget(factTargets, retraction.factID, path+".fact"); err != nil {
				return err
			}
		case retractionExact:
			if err := validateLabel(retraction.subject, path+".where.subject"); err != nil {
				return err
			}
			if err := validateLabel(retraction.predicate, path+".where.predicate"); err != nil {
				return err
			}
			if err := validateObject(retraction.object, path+".where.object"); err != nil {
				return err
			}
		case retractionSlot:
			if err := validateLabel(retraction.subject, path+".slot.subject"); err != nil {
				return err
			}
			if err := validateLabel(retraction.predicate, path+".slot.predicate"); err != nil {
				return err
			}
		default:
			return invalid(CodeMissingField, path,
				"retraction requires fact, exact-triple, or slot selector")
		}
	}

	for i, correction := range request.Corrections {
		path := pathIndex("correct", i)
		if err := validateFactID(correction.factID, path+".supersede"); err != nil {
			return err
		}
		if err := claimFactTarget(factTargets, correction.factID, path+".supersede"); err != nil {
			return err
		}
		if err := validateRecord(correction.replacement, path+".with"); err != nil {
			return err
		}
		if correction.replacement.Mode != Append {
			return invalid(CodeIncompatibleFields, path+".with.mode",
				"correction replacements do not accept a record mode")
		}
	}

	for i, expiration := range request.Expirations {
		path := pathIndex("expire", i)
		if err := validateFactID(expiration.factID, path+".fact"); err != nil {
			return err
		}
		if err := claimFactTarget(factTargets, expiration.factID, path+".fact"); err != nil {
			return err
		}
		if expiration.deadline.tag == deadlineUnset {
			return invalid(CodeMissingField, path+".expiration",
				"expiration requires a relative or absolute deadline")
		}
		if err := validateDeadline(expiration.deadline, path+".expiration"); err != nil {
			return err
		}
	}

	for i, persistence := range request.Persistence {
		path := pathIndex("persist", i)
		if err := validateFactID(persistence.factID, path+".fact"); err != nil {
			return err
		}
		if err := claimFactTarget(factTargets, persistence.factID, path+".fact"); err != nil {
			return err
		}
	}

	if err := validateTransactionTime(request.TransactionTime); err != nil {
		return err
	}
	return validateOwnership(request)
}

func validateRecord(record Record, path string) error {
	if err := validateLabel(record.Subject, path+".subject"); err != nil {
		return err
	}
	if err := validateLabel(record.Predicate, path+".predicate"); err != nil {
		return err
	}
	if err := validateObject(record.Object, path+".object"); err != nil {
		return err
	}
	if record.Tense != "" {
		if err := validateLabel(record.Tense, path+".tense"); err != nil {
			return err
		}
	}
	if !utf8.ValidString(record.RawText) {
		return invalid(CodeInvalidUTF8, path+".raw_text", "must contain valid UTF-8")
	}
	switch record.Mode {
	case Append:
	case Ensure, Replace:
		if record.Tense != "" || record.RawText != "" ||
			record.Expiration.tag != deadlineUnset {
			return invalid(CodeIncompatibleFields, path+".mode",
				"ensure and replace do not accept tense, raw_text, or expiration")
		}
	default:
		return invalid(CodeUnsupportedValue, path+".mode",
			"must be append, ensure, or replace")
	}
	return validateDeadline(record.Expiration, path+".expiration")
}

func validateObject(object Object, path string) error {
	switch object.tag {
	case objectEntity:
		return validateLabel(object.label, path)
	case objectNumber:
		if object.number > MaxJSSafeInteger {
			return invalid(CodeNumberOutOfRange, path,
				"numeric object exceeds the JavaScript-safe limit")
		}
		return nil
	default:
		return invalid(CodeInvalidObject, path,
			"object requires write.Entity or write.Number")
	}
}

func validateDeadline(deadline Deadline, path string) error {
	switch deadline.tag {
	case deadlineUnset:
		return nil
	case deadlineAfter:
		if deadline.duration <= 0 {
			return invalid(CodeInvalidDuration, path,
				"relative expiration must be positive")
		}
		if deadline.duration%time.Millisecond != 0 {
			return invalid(CodeInvalidDuration, path,
				"relative expiration must be exactly representable in milliseconds")
		}
		milliseconds := uint64(deadline.duration / time.Millisecond)
		if milliseconds > uint64(math.MaxInt64)/1_000_000 {
			return invalid(CodeInvalidDuration, path,
				"relative expiration overflows JoeyDB nanoseconds")
		}
		return nil
	case deadlineAt:
		nanoseconds, ok := exactUnixNano(deadline.at)
		if !ok || nanoseconds <= 0 {
			return invalid(CodeInvalidTime, path,
				"absolute expiration must be a positive exact Unix-nanosecond time")
		}
		return nil
	default:
		return invalid(CodeInvalidTime, path, "deadline variant is invalid")
	}
}

func validateTransactionTime(value TransactionTime) error {
	switch value.tag {
	case transactionTimeUnset, transactionTimeNanoseconds:
		return nil
	case transactionTimeAt:
		if _, ok := exactUnixNano(value.at); !ok {
			return invalid(CodeInvalidTime, "tx_time",
				"transaction time is outside the exact Unix-nanosecond range")
		}
		return nil
	default:
		return invalid(CodeInvalidTime, "tx_time", "transaction time variant is invalid")
	}
}

func exactUnixNano(value time.Time) (int64, bool) {
	nanoseconds := value.UnixNano()
	return nanoseconds, time.Unix(0, nanoseconds).Equal(value)
}

func validateLabel(value, path string) error {
	if value == "" {
		return invalid(CodeMissingField, path, "label must not be empty")
	}
	if !utf8.ValidString(value) {
		return invalid(CodeInvalidUTF8, path, "label must contain valid UTF-8")
	}
	return nil
}

func validateFactID(value, path string) error {
	if value == "" {
		return invalid(CodeInvalidFactID, path,
			"fact ID must be a positive canonical decimal u64")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || strconv.FormatUint(parsed, 10) != value {
		return invalid(CodeInvalidFactID, path,
			"fact ID must be a positive canonical decimal u64")
	}
	return nil
}

func claimFactTarget(targets map[string]string, factID, path string) error {
	if previous, exists := targets[factID]; exists {
		return invalid(CodeDuplicateTarget, path,
			"fact is already targeted by "+previous)
	}
	targets[factID] = path
	return nil
}

func pathIndex(name string, index int) string {
	return name + "[" + strconv.Itoa(index) + "]"
}

type slotKey struct {
	subject   string
	predicate string
}

type exactKey struct {
	slot   slotKey
	object objectKey
}

type objectKey struct {
	tag    objectTag
	label  string
	number uint64
}

func keyForObject(object Object) objectKey {
	return objectKey(object)
}

type ownership struct {
	slotOwner  string
	exactOwner map[objectKey]string
}

func validateOwnership(request Request) error {
	owned := make(map[slotKey]*ownership)
	claimSlot := func(slot slotKey, path string) error {
		entry := owned[slot]
		if entry == nil {
			entry = &ownership{exactOwner: make(map[objectKey]string)}
			owned[slot] = entry
		}
		switch {
		case entry.slotOwner != "":
			return invalid(CodeDuplicateTarget, path,
				"slot is already targeted by "+entry.slotOwner)
		case len(entry.exactOwner) != 0:
			return invalid(CodeDestinationConflict, path,
				"slot overlaps an exact-triple mutation")
		default:
			entry.slotOwner = path
			return nil
		}
	}
	claimExact := func(key exactKey, path string) error {
		entry := owned[key.slot]
		if entry == nil {
			entry = &ownership{exactOwner: make(map[objectKey]string)}
			owned[key.slot] = entry
		}
		if entry.slotOwner != "" {
			return invalid(CodeDestinationConflict, path,
				"exact triple overlaps slot mutation "+entry.slotOwner)
		}
		if previous := entry.exactOwner[key.object]; previous != "" {
			return invalid(CodeDuplicateTarget, path,
				"exact triple is already targeted by "+previous)
		}
		entry.exactOwner[key.object] = path
		return nil
	}

	for i, record := range request.Records {
		slot := slotKey{subject: record.Subject, predicate: record.Predicate}
		switch record.Mode {
		case Ensure:
			if err := claimExact(exactKey{slot: slot, object: keyForObject(record.Object)},
				pathIndex("record", i)); err != nil {
				return err
			}
		case Replace:
			if err := claimSlot(slot, pathIndex("record", i)); err != nil {
				return err
			}
		}
	}
	for i, retraction := range request.Retractions {
		slot := slotKey{subject: retraction.subject, predicate: retraction.predicate}
		switch retraction.tag {
		case retractionExact:
			if err := claimExact(exactKey{slot: slot, object: keyForObject(retraction.object)},
				pathIndex("retract", i)); err != nil {
				return err
			}
		case retractionSlot:
			if err := claimSlot(slot, pathIndex("retract", i)); err != nil {
				return err
			}
		}
	}

	checkDestination := func(record Record, path string) error {
		entry := owned[slotKey{subject: record.Subject, predicate: record.Predicate}]
		if entry == nil {
			return nil
		}
		if entry.slotOwner != "" {
			return invalid(CodeDestinationConflict, path,
				"destination overlaps slot mutation "+entry.slotOwner)
		}
		if owner := entry.exactOwner[keyForObject(record.Object)]; owner != "" {
			return invalid(CodeDestinationConflict, path,
				"destination overlaps exact-triple mutation "+owner)
		}
		return nil
	}
	for i, record := range request.Records {
		if record.Mode == Append {
			if err := checkDestination(record, pathIndex("record", i)); err != nil {
				return err
			}
		}
	}
	for i, correction := range request.Corrections {
		if err := checkDestination(correction.replacement,
			pathIndex("correct", i)+".with"); err != nil {
			return err
		}
	}
	return nil
}

// Features returns the request's minimal advertised capability requirements.
func (request Request) Features() Features {
	var needCorrect, needExpire, needPersist, needRecord, needRetract bool
	var needEntity, needNumber, needAbsolute, needRelative bool
	var needAppend, needEnsure, needReplace bool
	var needFact, needSlot, needWhere bool

	visitObject := func(object Object) {
		switch object.tag {
		case objectEntity:
			needEntity = true
		case objectNumber:
			needNumber = true
		}
	}
	visitDeadline := func(deadline Deadline) {
		switch deadline.tag {
		case deadlineAfter:
			needRelative = true
		case deadlineAt:
			needAbsolute = true
		}
	}
	for _, record := range request.Records {
		needRecord = true
		visitObject(record.Object)
		visitDeadline(record.Expiration)
		switch record.Mode {
		case Ensure:
			needEnsure = true
		case Replace:
			needReplace = true
		default:
			needAppend = true
		}
	}
	for _, retraction := range request.Retractions {
		needRetract = true
		switch retraction.tag {
		case retractionFact:
			needFact = true
		case retractionExact:
			needWhere = true
			visitObject(retraction.object)
		case retractionSlot:
			needSlot = true
		}
	}
	for _, correction := range request.Corrections {
		needCorrect = true
		visitObject(correction.replacement.Object)
		visitDeadline(correction.replacement.Expiration)
	}
	for _, expiration := range request.Expirations {
		needExpire = true
		visitDeadline(expiration.deadline)
	}
	needPersist = len(request.Persistence) > 0

	var result Features
	if needCorrect {
		result.Operations = append(result.Operations, OperationCorrect)
	}
	if needExpire {
		result.Operations = append(result.Operations, OperationExpire)
	}
	if needPersist {
		result.Operations = append(result.Operations, OperationPersist)
	}
	if needRecord {
		result.Operations = append(result.Operations, OperationRecord)
	}
	if needRetract {
		result.Operations = append(result.Operations, OperationRetract)
	}
	if needEntity {
		result.ObjectKinds = append(result.ObjectKinds, ObjectEntityLabel)
	}
	if needNumber {
		result.ObjectKinds = append(result.ObjectKinds, ObjectU64)
	}
	if needAbsolute {
		result.ExpirationForms = append(result.ExpirationForms, ExpirationAbsolute)
	}
	if needRelative {
		result.ExpirationForms = append(result.ExpirationForms, ExpirationRelative)
	}
	if request.Vocabulary != "" {
		result.VocabularyModes = append(result.VocabularyModes, request.Vocabulary)
	}
	if needAppend {
		result.RecordModes = append(result.RecordModes, Append)
	}
	if needEnsure {
		result.RecordModes = append(result.RecordModes, Ensure)
	}
	if needReplace {
		result.RecordModes = append(result.RecordModes, Replace)
	}
	if needFact {
		result.RetractionSelectors = append(result.RetractionSelectors, SelectorFact)
	}
	if needSlot {
		result.RetractionSelectors = append(result.RetractionSelectors, SelectorSlot)
	}
	if needWhere {
		result.RetractionSelectors = append(result.RetractionSelectors, SelectorWhere)
	}
	return result
}
