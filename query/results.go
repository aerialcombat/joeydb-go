package query

// ObjectKind identifies the wire representation of a fact object.
type ObjectKind string

const (
	// ObjectEntity means Object is an entity label and ObjectID identifies it.
	ObjectEntity ObjectKind = "entity"
	// ObjectNumber means Object is a canonical decimal u64 and ObjectID is "0".
	ObjectNumber ObjectKind = "number"
)

// Fact is JoeyDB's stable fact representation in query responses.
//
// IDs are canonical decimal strings. For numeric objects, Object contains the
// decimal value, ObjectKind is ObjectNumber, and ObjectID is "0".
type Fact struct {
	ID          string     `json:"id"`
	SubjectID   string     `json:"subject_id"`
	Subject     string     `json:"subject"`
	PredicateID string     `json:"predicate_id"`
	Predicate   string     `json:"predicate"`
	ObjectID    string     `json:"object_id"`
	Object      string     `json:"object"`
	ObjectKind  ObjectKind `json:"object_kind"`
	Tense       string     `json:"tense"`
	RawText     string     `json:"raw_text"`
}

// AppliedOrder is one normalized result-order key echoed by JoeyDB.
type AppliedOrder struct {
	By  string `json:"by"`
	Dir string `json:"dir,omitempty"`
}

// PageInfo describes an ordered or offset result window. Exactly one returned
// count is present for a supported result shape.
type PageInfo struct {
	Offset               int    `json:"offset"`
	ReturnedFactCount    *int   `json:"returned_fact_count,omitempty"`
	ReturnedBindingCount *int   `json:"returned_binding_count,omitempty"`
	ReturnedGroupCount   *int   `json:"returned_group_count,omitempty"`
	NextCursor           string `json:"next_cursor,omitempty"`
}

// PlanCandidate is one physical route considered by JoeyDB's planner.
type PlanCandidate struct {
	Route            string `json:"route"`
	Fresh            bool   `json:"fresh"`
	Eligible         bool   `json:"eligible"`
	EstimatedFacts   int    `json:"estimated_facts"`
	Watermark        uint64 `json:"watermark,omitempty"`
	IneligibleReason string `json:"ineligible_reason,omitempty"`
}

// PlanStage describes one executed stage and its stable accounting fields.
// Pointer counts preserve JoeyDB's distinction between an inapplicable field
// and a present zero.
type PlanStage struct {
	Pattern          *int   `json:"pattern,omitempty"`
	Operator         string `json:"operator,omitempty"`
	Representation   string `json:"representation"`
	AccessPath       string `json:"access_path"`
	FactsConsidered  *int   `json:"facts_considered,omitempty"`
	AssignmentsOut   *int   `json:"assignments_out,omitempty"`
	StaleSkipped     int    `json:"stale_skipped,omitempty"`
	RescuedLive      int    `json:"rescued_live,omitempty"`
	Direction        string `json:"direction,omitempty"`
	MinDepth         *int   `json:"min_depth,omitempty"`
	MaxDepth         *int   `json:"max_depth,omitempty"`
	Uniqueness       string `json:"uniqueness,omitempty"`
	StartCount       *int   `json:"start_count,omitempty"`
	PostingsExamined *int   `json:"postings_examined,omitempty"`
	EdgesConsidered  *int   `json:"edges_considered,omitempty"`
	EdgesAdmitted    *int   `json:"edges_admitted,omitempty"`
	PathsPruned      *int   `json:"paths_pruned,omitempty"`
	FrontierPeak     *int   `json:"frontier_peak,omitempty"`
	ArenaBytesPeak   *int   `json:"arena_bytes_peak,omitempty"`
	PathsEmitted     *int   `json:"paths_emitted,omitempty"`
}

// Plan is JoeyDB's machine-readable planner decision.
type Plan struct {
	Chosen            string          `json:"chosen"`
	Reason            string          `json:"reason"`
	Candidates        []PlanCandidate `json:"candidates"`
	RequiredWatermark uint64          `json:"required_watermark,omitempty"`
	Stages            []PlanStage     `json:"stages,omitempty"`
	ExecutionOrder    []int           `json:"execution_order,omitempty"`
	OrderFallback     bool            `json:"order_fallback,omitempty"`
	Built             []string        `json:"built,omitempty"`
	Materialization   string          `json:"materialization,omitempty"`
}

// Metadata contains the stable common metadata on fact-shaped query results.
// String fields intentionally accept unknown future values.
type Metadata struct {
	ServedBy                string         `json:"served_by"`
	RequestedRepresentation string         `json:"requested_representation,omitempty"`
	OptimizeMode            string         `json:"optimize_mode"`
	RequestedConsistency    string         `json:"requested_consistency"`
	ServedConsistency       string         `json:"served_consistency"`
	Source                  string         `json:"source"`
	FactCount               int            `json:"fact_count"`
	Folded                  bool           `json:"folded,omitempty"`
	Truncated               bool           `json:"truncated,omitempty"`
	ReturnedFactCount       int            `json:"returned_fact_count,omitempty"`
	ReturnedBindingCount    int            `json:"returned_binding_count,omitempty"`
	ReturnedPathCount       int            `json:"returned_path_count,omitempty"`
	Order                   []AppliedOrder `json:"order,omitempty"`
	Page                    *PageInfo      `json:"page,omitempty"`
	AccessPath              string         `json:"access_path,omitempty"`
	Watermark               uint64         `json:"watermark,omitempty"`
	StoreVersion            uint64         `json:"store_version,omitempty"`
	Plan                    *Plan          `json:"plan,omitempty"`
}

// Timing is JoeyDB's per-query wall-clock accounting in nanoseconds.
type Timing struct {
	PlanNS       int64 `json:"plan_ns"`
	BuildNS      int64 `json:"build_ns"`
	AdaptationNS int64 `json:"adaptation_ns,omitempty"`
	ExecuteNS    int64 `json:"execute_ns"`
	TotalNS      int64 `json:"total_ns"`
}

// Result contains the fields common to all five simple fact-shaped results.
// Facts is nil when JoeyDB omits it and may be empty when it is present with no
// matches; callers should use their authored fact-inclusion mode as the
// semantic source of truth.
type Result struct {
	Shape    Shape    `json:"shape"`
	Facts    []Fact   `json:"facts"`
	Metadata Metadata `json:"metadata"`
	Timing   *Timing  `json:"timing,omitempty"`
}

// GraphNode is one entity in a graph payload.
type GraphNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// GraphEdge is one fact-backed directed edge in a graph payload.
type GraphEdge struct {
	FactID      string `json:"fact_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	PredicateID string `json:"predicate_id"`
	Predicate   string `json:"predicate"`
	Tense       string `json:"tense"`
	RawText     string `json:"raw_text"`
}

// GraphPayload is the graph-shaped result body.
type GraphPayload struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphResult is a shape-safe graph query result.
type GraphResult struct {
	Result
	Graph *GraphPayload `json:"graph"`
}

// TableRow is one stable row in a table payload.
type TableRow struct {
	FactID   string `json:"fact_id"`
	Actor    string `json:"actor"`
	Action   string `json:"action"`
	Target   string `json:"target"`
	Tense    string `json:"tense"`
	RawText  string `json:"raw_text"`
	ActorID  string `json:"actor_id"`
	TargetID string `json:"target_id"`
	ActionID string `json:"action_id"`
}

// TablePayload is the table-shaped result body.
type TablePayload struct {
	Rows []TableRow `json:"rows"`
}

// TableResult is a shape-safe table query result.
type TableResult struct {
	Result
	Table *TablePayload `json:"table"`
}

// DocumentEntity is one labeled entity in a document payload.
type DocumentEntity struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// DocumentAttribute is one ordered document key/value pair.
type DocumentAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// DocumentFact is one fact in a document payload.
type DocumentFact struct {
	ID     string              `json:"id"`
	Actor  DocumentEntity      `json:"actor"`
	Action DocumentEntity      `json:"action"`
	Target DocumentEntity      `json:"target"`
	Attrs  []DocumentAttribute `json:"attrs"`
	Raw    []DocumentAttribute `json:"raw"`
}

// DocumentPayload is the document-shaped result body.
type DocumentPayload struct {
	Facts []DocumentFact `json:"facts"`
}

// DocumentResult is a shape-safe document query result.
type DocumentResult struct {
	Result
	Document *DocumentPayload `json:"document"`
}

// KVFactEntry is one fact-ID-keyed entry in a key/value payload.
type KVFactEntry struct {
	Key  string `json:"key"`
	Fact Fact   `json:"fact"`
}

// KVIndexEntry is one entity-label-keyed collection in a key/value payload.
type KVIndexEntry struct {
	Key   string `json:"key"`
	Facts []Fact `json:"facts"`
}

// KVPayload is the key/value-shaped result body.
type KVPayload struct {
	ByFact      []KVFactEntry  `json:"by_fact"`
	BySubject   []KVIndexEntry `json:"by_subject"`
	ByPredicate []KVIndexEntry `json:"by_predicate"`
	ByObject    []KVIndexEntry `json:"by_object"`
}

// KVResult is a shape-safe key/value query result.
type KVResult struct {
	Result
	KV *KVPayload `json:"kv"`
}

// ColumnarPayload stores aligned columns for one columnar result.
type ColumnarPayload struct {
	FactIDs      []string `json:"fact_ids"`
	Subjects     []string `json:"subjects"`
	Predicates   []string `json:"predicates"`
	Objects      []string `json:"objects"`
	Tenses       []string `json:"tenses"`
	RawTexts     []string `json:"raw_texts"`
	SubjectIDs   []string `json:"subject_ids"`
	PredicateIDs []string `json:"predicate_ids"`
	ObjectIDs    []string `json:"object_ids"`
}

// ColumnarResult is a shape-safe columnar query result.
type ColumnarResult struct {
	Result
	Columnar *ColumnarPayload `json:"columnar"`
}
