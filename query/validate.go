package query

import (
	"encoding/hex"
	"strconv"
	"unicode/utf8"
)

// Validate checks every state-independent rule in the typed query subset.
func Validate(request Request) error {
	return request.Validate()
}

// Validate checks every state-independent rule in the typed query subset.
func (request Request) Validate() error {
	if err := validateWhere(request.Where); err != nil {
		return err
	}
	if err := validateReturn(request.Return); err != nil {
		return err
	}
	if request.Where.ObjectNumber != nil {
		switch request.Return.shape {
		case ShapeTable, ShapeDocument, ShapeColumnar:
		default:
			return invalid(CodeIncompatibleFields, "where.object_number",
				"numeric ranges require a table, document, or columnar return")
		}
	}
	switch request.Consistency {
	case "", Strict, Fresh, AllowStale:
	default:
		return invalid(CodeUnsupportedValue, "consistency",
			"must be strict, fresh, or allow_stale")
	}
	if err := validateOptimization(request.Optimization); err != nil {
		return err
	}
	if err := validateReadConstraint(request.ReadConstraint); err != nil {
		return err
	}
	if request.Limit.set && request.Limit.value < 1 {
		return invalid(CodeInvalidLimit, "limit", "must be at least 1")
	}
	if request.Offset < 0 {
		return invalid(CodeInvalidOffset, "offset", "must be at least 0")
	}
	return validateOrder(request)
}

func validateWhere(where Where) error {
	hasPosition := where.Subject.set || where.Predicate.set ||
		where.Object.set || where.ObjectNumber != nil
	if where.all {
		if hasPosition {
			return invalid(CodeIncompatibleFields, "where",
				"query.All cannot be combined with filters")
		}
		return nil
	}
	if !hasPosition {
		return invalid(CodeUnsafeMatchAll, "where",
			"use query.All for an intentional match-all query")
	}
	if err := validateLabels(where.Subject, "where.subject"); err != nil {
		return err
	}
	if err := validateLabels(where.Predicate, "where.predicate"); err != nil {
		return err
	}
	if err := validateLabels(where.Object, "where.object"); err != nil {
		return err
	}
	if where.Object.set && where.ObjectNumber != nil {
		return invalid(CodeIncompatibleFields, "where.object_number",
			"entity object labels and numeric bounds are mutually exclusive")
	}
	if where.ObjectNumber != nil {
		if where.ObjectNumber.GT.set && where.ObjectNumber.GTE.set {
			return invalid(CodeIncompatibleFields, "where.object_number",
				"gt and gte are mutually exclusive")
		}
		if where.ObjectNumber.LT.set && where.ObjectNumber.LTE.set {
			return invalid(CodeIncompatibleFields, "where.object_number",
				"lt and lte are mutually exclusive")
		}
	}
	return nil
}

func validateLabels(labels LabelSet, path string) error {
	if !labels.set {
		return nil
	}
	if len(labels.values) == 0 {
		return invalid(CodeMissingField, path, "membership list must not be empty")
	}
	seen := make(map[string]struct{}, len(labels.values))
	for i, value := range labels.values {
		itemPath := path
		if len(labels.values) > 1 {
			itemPath += "[" + strconv.Itoa(i) + "]"
		}
		if value == "" {
			return invalid(CodeMissingField, itemPath, "label must not be empty")
		}
		if !utf8.ValidString(value) {
			return invalid(CodeInvalidUTF8, itemPath, "label must contain valid UTF-8")
		}
		if _, exists := seen[value]; exists {
			return invalid(CodeDuplicateValue, itemPath, "label is duplicated")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateReturn(result Return) error {
	if result.invalid {
		return invalid(CodeIncompatibleFields, "return.include_facts",
			"at most one fact-inclusion choice is allowed")
	}
	switch result.shape {
	case ShapeTable, ShapeGraph, ShapeDocument, ShapeKV, ShapeColumnar:
	default:
		return invalid(CodeMissingField, "return",
			"use a typed return constructor")
	}
	switch result.inclusion {
	case DefaultFacts, IncludeFacts, ExcludeFacts:
		return nil
	default:
		return invalid(CodeUnsupportedValue, "return.include_facts",
			"fact inclusion is unsupported")
	}
}

func validateOptimization(optimization Optimization) error {
	switch optimization.tag {
	case optimizationAuto:
		if optimization.representation != "" {
			return invalid(CodeIncompatibleFields, "optimize.representation",
				"automatic optimization does not accept a representation")
		}
		return nil
	case optimizationForce:
		switch optimization.representation {
		case PrimitiveScan, GraphView, TableView, DocumentView, KVView, ColumnarView:
			return nil
		default:
			return invalid(CodeUnsupportedValue, "optimize.representation",
				"forced representation is unsupported")
		}
	default:
		return invalid(CodeUnsupportedValue, "optimize.mode",
			"optimization mode is unsupported")
	}
}

func validateReadConstraint(constraint ReadConstraint) error {
	switch constraint.tag {
	case readConstraintUnset:
		return nil
	case readConstraintAfter:
		if constraint.watermark == 0 {
			return invalid(CodeMissingField, "required_watermark",
				"read-after watermark must be nonzero")
		}
	case readConstraintLog:
		if constraint.watermark != 0 {
			return invalid(CodeIncompatibleFields, "required_watermark",
				"log-only constraint cannot carry a watermark")
		}
	default:
		return invalid(CodeUnsupportedValue, "read_constraint",
			"read constraint variant is unsupported")
	}
	if !validLogIdentity(constraint.logIdentity) {
		return invalid(CodeInvalidIdentity, "log_identity",
			"must be 32 lowercase hexadecimal characters")
	}
	return nil
}

func validLogIdentity(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func validateOrder(request Request) error {
	if (len(request.Order) > 0 || request.Offset > 0) &&
		(request.Return.shape == ShapeGraph || request.Return.shape == ShapeKV) {
		return invalid(CodeIncompatibleFields, "order",
			"order and offset are not defined for graph or kv returns")
	}
	seen := make(map[OrderField]struct{}, len(request.Order))
	numeric := false
	for i, order := range request.Order {
		path := "order[" + strconv.Itoa(i) + "]"
		switch order.Direction {
		case "", Ascending, Descending:
		default:
			return invalid(CodeInvalidOrder, path+".direction",
				"must be asc or desc")
		}
		if _, exists := seen[order.By]; exists {
			return invalid(CodeDuplicateValue, path+".by", "order field is duplicated")
		}
		seen[order.By] = struct{}{}
		switch order.By {
		case ByID, BySubject, ByPredicate, ByObject:
		case ByObjectNumber:
			numeric = true
			if request.Where.ObjectNumber == nil {
				return invalid(CodeIncompatibleFields, path+".by",
					"object_number order requires where.object_number")
			}
		default:
			return invalid(CodeInvalidOrder, path+".by", "order field is unsupported")
		}
	}
	if numeric && len(request.Order) != 1 {
		return invalid(CodeIncompatibleFields, "order",
			"object_number must be the only order field")
	}
	return nil
}
