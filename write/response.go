package write

// Response is JoeyDB's facts-write receipt.
type Response struct {
	Committed       bool               `json:"committed"`
	Watermark       uint64             `json:"watermark"`
	LogIdentity     string             `json:"log_identity"`
	Facts           []FactResult       `json:"facts,omitempty"`
	Retracted       []string           `json:"retracted,omitempty"`
	Corrected       []CorrectResult    `json:"corrected,omitempty"`
	Expirations     []ExpirationResult `json:"expirations,omitempty"`
	CreatedEntities []CreatedEntity    `json:"created_entities,omitempty"`
	Logical         []LogicalResult    `json:"logical,omitempty"`
}

// FactResult identifies one recorded fact.
type FactResult struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

// CorrectResult identifies one superseded and replacement fact pair.
type CorrectResult struct {
	Superseded string `json:"superseded"`
	ID         string `json:"id"`
}

// ExpirationResult reports an expiration schedule mutation.
type ExpirationResult struct {
	Fact        string `json:"fact"`
	ExpiresAtNS string `json:"expires_at_ns,omitempty"`
	Changed     bool   `json:"changed"`
}

// CreatedEntity identifies a label created by vocabulary growth.
type CreatedEntity struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// LogicalResult reports one ensure, replace, or logical removal outcome.
type LogicalResult struct {
	Scope      string   `json:"scope"`
	Index      int      `json:"index"`
	Operation  string   `json:"operation"`
	Outcome    string   `json:"outcome"`
	ID         string   `json:"id,omitempty"`
	Superseded string   `json:"superseded,omitempty"`
	Removed    []string `json:"removed,omitempty"`
}
