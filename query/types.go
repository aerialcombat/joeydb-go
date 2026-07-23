package query

// LabelSet is one unconstrained, scalar, or membership-list query position.
// Use Labels to construct a present position.
type LabelSet struct {
	set    bool
	values []string
}

// Labels copies one or more entity labels for a query position. Validation
// rejects an empty argument list, empty labels, and duplicates.
func Labels(values ...string) LabelSet {
	return LabelSet{set: true, values: append([]string(nil), values...)}
}

// Values returns a defensive copy and whether this position was explicitly set.
func (labels LabelSet) Values() ([]string, bool) {
	return append([]string(nil), labels.values...), labels.set
}

// NumericBound is a presence-preserving u64 query bound.
type NumericBound struct {
	set   bool
	value uint64
}

// Bound constructs a present numeric bound, including a present zero.
func Bound(value uint64) NumericBound {
	return NumericBound{set: true, value: value}
}

// Value returns the bound and whether it is present.
func (bound NumericBound) Value() (uint64, bool) {
	return bound.value, bound.set
}

// NumericRange selects numeric-object values. An explicitly non-nil empty
// range selects all numeric objects.
type NumericRange struct {
	GT  NumericBound
	GTE NumericBound
	LT  NumericBound
	LTE NumericBound
}

// Where is the stable object-form facts filter.
type Where struct {
	Subject      LabelSet
	Predicate    LabelSet
	Object       LabelSet
	ObjectNumber *NumericRange
	all          bool
}

// All constructs an explicit match-all filter. A zero Where is rejected.
func All() Where {
	return Where{all: true}
}

// FactInclusion controls the top-level duplicate facts array for fact-shaped
// returns.
type FactInclusion uint8

const (
	DefaultFacts FactInclusion = iota
	IncludeFacts
	ExcludeFacts
)

// Shape is a supported simple fact-shaped return.
type Shape string

const (
	ShapeColumnar Shape = "columnar"
	ShapeDocument Shape = "document"
	ShapeGraph    Shape = "graph"
	ShapeKV       Shape = "kv"
	ShapeTable    Shape = "table"
)

// Return is a typed simple fact-shaped return request.
type Return struct {
	shape     Shape
	inclusion FactInclusion
	invalid   bool
}

// Table selects the table return shape.
func Table(inclusion ...FactInclusion) Return {
	return makeReturn(ShapeTable, inclusion)
}

// Graph selects the graph return shape.
func Graph(inclusion ...FactInclusion) Return {
	return makeReturn(ShapeGraph, inclusion)
}

// Document selects the document return shape.
func Document(inclusion ...FactInclusion) Return {
	return makeReturn(ShapeDocument, inclusion)
}

// KV selects the key/value return shape.
func KV(inclusion ...FactInclusion) Return {
	return makeReturn(ShapeKV, inclusion)
}

// Columnar selects the columnar return shape.
func Columnar(inclusion ...FactInclusion) Return {
	return makeReturn(ShapeColumnar, inclusion)
}

func makeReturn(shape Shape, inclusion []FactInclusion) Return {
	result := Return{shape: shape}
	switch len(inclusion) {
	case 0:
	case 1:
		result.inclusion = inclusion[0]
	default:
		result.invalid = true
	}
	return result
}

// Shape reports the return shape.
func (result Return) Shape() Shape { return result.shape }

// Inclusion reports the include_facts choice.
func (result Return) Inclusion() FactInclusion { return result.inclusion }

// Consistency is a JoeyDB query consistency mode. The zero value safely
// defaults to Strict.
type Consistency string

const (
	Strict     Consistency = "strict"
	Fresh      Consistency = "fresh"
	AllowStale Consistency = "allow_stale"
)

// Representation is a forceable JoeyDB physical representation.
type Representation string

const (
	PrimitiveScan Representation = "primitive_scan"
	GraphView     Representation = "graph"
	TableView     Representation = "table"
	DocumentView  Representation = "document"
	KVView        Representation = "kv"
	ColumnarView  Representation = "columnar"
)

type optimizationTag uint8

const (
	optimizationAuto optimizationTag = iota
	optimizationForce
)

// Optimization is either automatic planning or one forced representation.
// The zero value is automatic.
type Optimization struct {
	tag            optimizationTag
	representation Representation
}

// Automatic returns the explicit automatic-planning value.
func Automatic() Optimization { return Optimization{} }

// Force requires one advertised representation.
func Force(representation Representation) Optimization {
	return Optimization{tag: optimizationForce, representation: representation}
}

// ForcedRepresentation reports the forced route and whether this is force.
func (optimization Optimization) ForcedRepresentation() (Representation, bool) {
	return optimization.representation, optimization.tag == optimizationForce
}

type readConstraintTag uint8

const (
	readConstraintUnset readConstraintTag = iota
	readConstraintAfter
	readConstraintLog
)

// ReadConstraint safely scopes a watermark and/or query to one log identity.
type ReadConstraint struct {
	tag         readConstraintTag
	watermark   uint64
	logIdentity string
}

// ReadAfter requires a nonzero watermark on the named log.
func ReadAfter(watermark uint64, logIdentity string) ReadConstraint {
	return ReadConstraint{
		tag: readConstraintAfter, watermark: watermark, logIdentity: logIdentity,
	}
}

// OnLog pins the query to one log without a watermark floor.
func OnLog(logIdentity string) ReadConstraint {
	return ReadConstraint{tag: readConstraintLog, logIdentity: logIdentity}
}

// Values returns the constraint values and whether it is present.
func (constraint ReadConstraint) Values() (uint64, string, bool) {
	return constraint.watermark, constraint.logIdentity,
		constraint.tag != readConstraintUnset
}

// Limit is a presence-preserving result cap.
type Limit struct {
	set   bool
	value int
}

// MaxResults constructs a present result limit. Validation rejects values
// below one.
func MaxResults(value int) Limit {
	return Limit{set: true, value: value}
}

// Value returns the limit and whether it is present.
func (limit Limit) Value() (int, bool) { return limit.value, limit.set }

// OrderField is a fact field accepted by simple ordering.
type OrderField string

const (
	ByID           OrderField = "id"
	ByObject       OrderField = "object"
	ByObjectNumber OrderField = "object_number"
	ByPredicate    OrderField = "predicate"
	BySubject      OrderField = "subject"
)

// Direction is one order direction. The zero value means ascending.
type Direction string

const (
	Ascending  Direction = "asc"
	Descending Direction = "desc"
)

// Order is one sort key.
type Order struct {
	By        OrderField
	Direction Direction
}

// Request is the typed object-form JoeyDB facts query.
type Request struct {
	Where          Where
	Return         Return
	Consistency    Consistency
	Optimization   Optimization
	ReadConstraint ReadConstraint
	Limit          Limit
	Order          []Order
	Offset         int
}
