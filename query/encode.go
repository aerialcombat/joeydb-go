package query

import (
	"encoding/json"
	"strconv"
	"strings"
)

type decimalUint64 uint64

func (value decimalUint64) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(value), 10))
}

type wireRequest struct {
	Find              string           `json:"find"`
	Where             wireWhere        `json:"where"`
	Return            wireReturn       `json:"return"`
	Consistency       Consistency      `json:"consistency"`
	Optimize          wireOptimization `json:"optimize"`
	RequiredWatermark uint64           `json:"required_watermark,omitempty"`
	LogIdentity       string           `json:"log_identity,omitempty"`
	Limit             *int             `json:"limit,omitempty"`
	Order             []wireOrder      `json:"order,omitempty"`
	Offset            int              `json:"offset,omitempty"`
}

type wireWhere struct {
	Subject      any               `json:"subject,omitempty"`
	Predicate    any               `json:"predicate,omitempty"`
	Object       any               `json:"object,omitempty"`
	ObjectNumber *wireNumericRange `json:"object_number,omitempty"`
}

type wireNumericRange struct {
	GT  *decimalUint64 `json:"gt,omitempty"`
	GTE *decimalUint64 `json:"gte,omitempty"`
	LT  *decimalUint64 `json:"lt,omitempty"`
	LTE *decimalUint64 `json:"lte,omitempty"`
}

type wireReturn struct {
	Shape        Shape `json:"shape"`
	IncludeFacts *bool `json:"include_facts,omitempty"`
}

type wireOptimization struct {
	Mode           string         `json:"mode"`
	Representation Representation `json:"representation,omitempty"`
}

type wireOrder struct {
	By  OrderField `json:"by"`
	Dir Direction  `json:"dir,omitempty"`
}

// Encode validates and returns one deterministic compact JSON request.
func (request Request) Encode() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	wire := wireRequest{
		Find: "facts",
		Where: wireWhere{
			Subject:   labelsWire(request.Where.Subject),
			Predicate: labelsWire(request.Where.Predicate),
			Object:    labelsWire(request.Where.Object),
		},
		Return:      wireReturn{Shape: request.Return.shape},
		Consistency: request.Consistency,
		Optimize:    wireOptimization{Mode: "auto"},
		Offset:      request.Offset,
	}
	if wire.Consistency == "" {
		wire.Consistency = Strict
	}
	switch request.Return.inclusion {
	case IncludeFacts:
		value := true
		wire.Return.IncludeFacts = &value
	case ExcludeFacts:
		value := false
		wire.Return.IncludeFacts = &value
	}
	if request.Where.ObjectNumber != nil {
		wire.Where.ObjectNumber = numericRangeWire(*request.Where.ObjectNumber)
	}
	if representation, forced := request.Optimization.ForcedRepresentation(); forced {
		wire.Optimize.Mode = "force"
		wire.Optimize.Representation = representation
	}
	switch request.ReadConstraint.tag {
	case readConstraintAfter:
		wire.RequiredWatermark = request.ReadConstraint.watermark
		wire.LogIdentity = request.ReadConstraint.logIdentity
	case readConstraintLog:
		wire.LogIdentity = request.ReadConstraint.logIdentity
	}
	if request.Limit.set {
		value := request.Limit.value
		wire.Limit = &value
	}
	wire.Order = make([]wireOrder, 0, len(request.Order))
	for _, order := range request.Order {
		wire.Order = append(wire.Order, wireOrder{By: order.By, Dir: order.Direction})
	}
	return json.Marshal(wire)
}

// MarshalJSON validates and uses the same deterministic encoder as Encode.
func (request Request) MarshalJSON() ([]byte, error) {
	return request.Encode()
}

func labelsWire(labels LabelSet) any {
	if !labels.set {
		return nil
	}
	if len(labels.values) == 1 {
		return escapeLabel(labels.values[0])
	}
	values := make([]string, len(labels.values))
	for i, label := range labels.values {
		values[i] = escapeLabel(label)
	}
	return values
}

func escapeLabel(label string) string {
	if strings.HasPrefix(label, "?") {
		return "?" + label
	}
	return label
}

func numericRangeWire(source NumericRange) *wireNumericRange {
	result := &wireNumericRange{}
	if source.GT.set {
		value := decimalUint64(source.GT.value)
		result.GT = &value
	}
	if source.GTE.set {
		value := decimalUint64(source.GTE.value)
		result.GTE = &value
	}
	if source.LT.set {
		value := decimalUint64(source.LT.value)
		result.LT = &value
	}
	if source.LTE.set {
		value := decimalUint64(source.LTE.value)
		result.LTE = &value
	}
	return result
}
