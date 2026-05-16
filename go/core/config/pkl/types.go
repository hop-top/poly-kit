package pkl

// FieldType represents the type of a config field.
type FieldType int

const (
	TypeString FieldType = iota
	TypeInt
	TypeFloat
	TypeBool
	TypeDuration
	TypeStringEnum
	TypeStringList
)

// ConstraintKind identifies the type of constraint.
type ConstraintKind int

const (
	ConstraintMinLen ConstraintKind = iota
	ConstraintMaxLen
	ConstraintMin
	ConstraintMax
	ConstraintPattern
	ConstraintBetween
)

// Constraint represents a validation constraint on a field.
type Constraint struct {
	Kind  ConstraintKind
	Value any // int for min/max/len, string for pattern, [2]int for between
}

// FieldCondition represents a when-condition for conditional visibility.
type FieldCondition struct {
	Expression string   // raw expression: "lang != \"go\""
	DependsOn  []string // field paths referenced
}

// FieldDef describes a single config field extracted from a PKL schema.
type FieldDef struct {
	Path        string // dotted key path: "database.port"
	Type        FieldType
	Description string   // from /// doc comments
	Default     any      // evaluated default value
	Required    bool     // non-nullable in PKL
	Enum        []string // for union types
	Constraints []Constraint
	Group       string          // from @wizard.group annotation
	When        *FieldCondition // from @wizard.when annotation
	Computed    bool            // has cross-field expression
}

// Schema holds all field definitions extracted from a PKL module.
type Schema struct {
	ModuleName string
	Fields     []FieldDef
}
