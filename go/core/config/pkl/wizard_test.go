package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/wizard"
)

func wizardSchema() *Schema {
	return &Schema{
		ModuleName: "TestConfig",
		Fields: []FieldDef{
			{Path: "name", Type: TypeString, Required: true,
				Description: "app name", Group: "basics",
				Default: "myapp"},
			{Path: "lang", Type: TypeStringEnum,
				Enum:        []string{"go", "py", "ts"},
				Description: "language"},
			{Path: "debug", Type: TypeBool,
				Description: "enable debug"},
			{Path: "port", Type: TypeInt,
				Constraints: []Constraint{
					{Kind: ConstraintBetween, Value: [2]int{1024, 65535}},
				}},
			{Path: "derived", Type: TypeString, Computed: true},
		},
	}
}

func TestWizardSteps_String(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "name")
	require.NotNil(t, s, "expected step for 'name'")
	assert.Equal(t, wizard.KindTextInput, s.Kind)
}

func TestWizardSteps_Enum(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "lang")
	require.NotNil(t, s)
	assert.Equal(t, wizard.KindSelect, s.Kind)
	assert.Len(t, s.Options, 3)
}

func TestWizardSteps_Bool(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "debug")
	require.NotNil(t, s)
	assert.Equal(t, wizard.KindConfirm, s.Kind)
}

func TestWizardSteps_Required(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "name")
	require.NotNil(t, s)
	assert.True(t, s.Required)
}

func TestWizardSteps_Default(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "name")
	require.NotNil(t, s)
	assert.Equal(t, "myapp", s.DefaultValue)
}

func TestWizardSteps_Group(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "name")
	require.NotNil(t, s)
	assert.Equal(t, "basics", s.Group)
}

func TestWizardSteps_SkipsComputed(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "derived")
	assert.Nil(t, s, "computed field must not produce a step")
}

func TestWizardSteps_IntValidator(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)

	s := findStep(steps, "port")
	require.NotNil(t, s)
	assert.Equal(t, wizard.KindTextInput, s.Kind)
	require.NotNil(t, s.ValidateText,
		"int field should have text validator")
}

func TestWizardSteps_Count(t *testing.T) {
	steps, err := WizardSteps(wizardSchema())
	require.NoError(t, err)
	// 5 fields minus 1 computed = 4 steps
	assert.Len(t, steps, 4)
}

func TestWizardSteps_BoolDefaultFromString(t *testing.T) {
	schema := &Schema{
		ModuleName: "Test",
		Fields: []FieldDef{
			{Path: "debug", Type: TypeBool, Default: "true",
				Description: "enable debug"},
		},
	}
	steps, err := WizardSteps(schema)
	require.NoError(t, err)
	s := findStep(steps, "debug")
	require.NotNil(t, s)
	assert.Equal(t, true, s.DefaultValue,
		"string 'true' must coerce to bool true")
}

func TestWizardSteps_WhenFullExpression(t *testing.T) {
	schema := &Schema{
		ModuleName: "Test",
		Fields: []FieldDef{
			{Path: "lang", Type: TypeStringEnum,
				Enum: []string{"go", "py"}},
			{Path: "gofmt", Type: TypeBool,
				Description: "run gofmt",
				When: &FieldCondition{
					Expression: `lang != "go"`,
					DependsOn:  []string{"lang"},
				}},
		},
	}
	steps, err := WizardSteps(schema)
	require.NoError(t, err)
	s := findStep(steps, "gofmt")
	require.NotNil(t, s)
	require.NotNil(t, s.When,
		"step should have a when condition")
	assert.Equal(t, "lang", s.When.Key)
	// lang != "go" → predicate returns false when value is "go"
	assert.False(t, s.When.Pred("go"))
	// lang != "go" → predicate returns true when value is "py"
	assert.True(t, s.When.Pred("py"))
}

func findStep(steps []wizard.Step, key string) *wizard.Step {
	for i := range steps {
		if steps[i].Key == key {
			return &steps[i]
		}
	}
	return nil
}
