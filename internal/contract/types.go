package contract

type ExecutionSpec struct {
	PlanRequired        bool `json:"planRequired"`
	ApprovalRequired    bool `json:"approvalRequired,omitempty"`
	ApprovalAction      bool `json:"approvalAction,omitempty"`
	Asynchronous        bool `json:"asynchronous"`
	IdempotencyRequired bool `json:"idempotencyRequired"`
}

type CommandMetadata struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Package     string `json:"package"`
	Description string `json:"description"`
}

type ErrorCatalogEntry struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	Category  string `json:"category"`
	Message   string `json:"message"`
}

type PaginationSpec struct {
	Supported       bool `json:"supported"`
	DefaultLimit    int  `json:"defaultLimit,omitempty"`
	MaxLimit        int  `json:"maxLimit,omitempty"`
	CursorSupported bool `json:"cursorSupported,omitempty"`
}

type CommandSpec struct {
	Path               []string            `json:"path"`
	Operation          string              `json:"operation"`
	InputSchema        string              `json:"inputSchema"`
	OutputSchema       string              `json:"outputSchema"`
	RequiredCapability string              `json:"requiredCapability"`
	ScopeTypes         []string            `json:"scopeTypes"`
	Risk               string              `json:"risk"`
	Visibility         string              `json:"visibility,omitempty"`
	DataClassification string              `json:"dataClassification,omitempty"`
	Pagination         *PaginationSpec     `json:"pagination,omitempty"`
	Execution          ExecutionSpec       `json:"execution"`
	UI                 map[string]string   `json:"ui,omitempty"`
	ErrorCatalog       []ErrorCatalogEntry `json:"errorCatalog"`
	AuditRequired      bool                `json:"auditRequired"`
}

type CommandDescriptor struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   CommandMetadata `json:"metadata"`
	Spec       CommandSpec     `json:"spec"`
}

type SchemaProperty struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	MinLength   *int        `json:"minLength,omitempty"`
	MaxLength   *int        `json:"maxLength,omitempty"`
	Pattern     string      `json:"pattern,omitempty"`
	Minimum     *int        `json:"minimum,omitempty"`
	Maximum     *int        `json:"maximum,omitempty"`
}

type InputSchema struct {
	Schema               string                    `json:"$schema"`
	ID                   string                    `json:"$id"`
	Title                string                    `json:"title"`
	Type                 string                    `json:"type"`
	Required             []string                  `json:"required,omitempty"`
	AdditionalProperties bool                      `json:"additionalProperties"`
	Properties           map[string]SchemaProperty `json:"properties"`
}

type ContractBundle struct {
	Descriptors []CommandDescriptor    `json:"descriptors"`
	Schemas     map[string]InputSchema `json:"schemas"`
}
