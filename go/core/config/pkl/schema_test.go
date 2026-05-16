package pkl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseField_String(t *testing.T) {
	fd := parseField("name: String", nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if fd.Type != TypeString {
		t.Errorf("type = %d, want TypeString", fd.Type)
	}
	if !fd.Required {
		t.Error("expected Required=true")
	}
}

func TestParseField_StringWithDefault(t *testing.T) {
	fd := parseField(`name: String = "foo"`, nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if fd.Default != "foo" {
		t.Errorf("default = %v, want foo", fd.Default)
	}
}

func TestParseField_Enum(t *testing.T) {
	fd := parseField(`lang: "go"|"py"`, nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if fd.Type != TypeStringEnum {
		t.Errorf("type = %d, want TypeStringEnum", fd.Type)
	}
	if len(fd.Enum) != 2 || fd.Enum[0] != "go" || fd.Enum[1] != "py" {
		t.Errorf("enum = %v, want [go py]", fd.Enum)
	}
}

func TestParseField_Int(t *testing.T) {
	fd := parseField("port: Int = 5432", nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if fd.Type != TypeInt {
		t.Errorf("type = %d, want TypeInt", fd.Type)
	}
	if fd.Default != 5432 {
		t.Errorf("default = %v, want 5432", fd.Default)
	}
}

func TestParseField_Bool(t *testing.T) {
	fd := parseField("enabled: Boolean = true", nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if fd.Type != TypeBool {
		t.Errorf("type = %d, want TypeBool", fd.Type)
	}
	if fd.Default != true {
		t.Errorf("default = %v, want true", fd.Default)
	}
}

func TestParseField_Nullable(t *testing.T) {
	fd := parseField("db: String?", nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if fd.Required {
		t.Error("expected Required=false for nullable")
	}
}

func TestParseField_Constraint(t *testing.T) {
	fd := parseField("port: Int(isBetween(1024, 65535))", nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if fd.Type != TypeInt {
		t.Errorf("type = %d, want TypeInt", fd.Type)
	}
	if len(fd.Constraints) != 1 {
		t.Fatalf("constraints len = %d, want 1", len(fd.Constraints))
	}
	c := fd.Constraints[0]
	if c.Kind != ConstraintBetween {
		t.Errorf("kind = %d, want ConstraintBetween", c.Kind)
	}
	v := c.Value.([2]int)
	if v[0] != 1024 || v[1] != 65535 {
		t.Errorf("between = %v, want [1024 65535]", v)
	}
}

func TestParseAnnotations_Group(t *testing.T) {
	comments := []string{`/// @wizard.group "Setup"`}
	group, _, _ := parseAnnotations(comments)
	if group != "Setup" {
		t.Errorf("group = %q, want Setup", group)
	}
}

func TestParseAnnotations_When(t *testing.T) {
	comments := []string{`/// @wizard.when lang != "go"`}
	_, when, _ := parseAnnotations(comments)
	if when == nil {
		t.Fatal("expected when condition")
	}
	if when.Expression != `lang != "go"` {
		t.Errorf("expr = %q", when.Expression)
	}
	if len(when.DependsOn) == 0 || when.DependsOn[0] != "lang" {
		t.Errorf("deps = %v, want [lang]", when.DependsOn)
	}
}

func TestParseField_Computed(t *testing.T) {
	fd := parseField(`db_name: String = "#{name}_db"`, nil)
	if fd == nil {
		t.Fatal("expected field")
	}
	if !fd.Computed {
		t.Error("expected Computed=true")
	}
}

func TestLoadSchema_FullModule(t *testing.T) {
	src := `module MyConfig

name: String
lang: "go"|"python"|"typescript"
port: Int = 5432
enabled: Boolean = true
`
	dir := t.TempDir()
	p := filepath.Join(dir, "config.pkl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSchema(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.ModuleName != "MyConfig" {
		t.Errorf("module = %q", s.ModuleName)
	}
	if len(s.Fields) != 4 {
		t.Fatalf("fields = %d, want 4", len(s.Fields))
	}
	if s.Fields[0].Path != "name" || s.Fields[0].Type != TypeString {
		t.Errorf("field[0] = %+v", s.Fields[0])
	}
	if s.Fields[1].Type != TypeStringEnum || len(s.Fields[1].Enum) != 3 {
		t.Errorf("field[1] = %+v", s.Fields[1])
	}
}

func TestLoadSchema_NestedClass(t *testing.T) {
	src := `module App

class Database {
  host: String = "localhost"
  port: Int = 5432
}

db: Database
`
	dir := t.TempDir()
	p := filepath.Join(dir, "app.pkl")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSchema(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Fields) != 2 {
		t.Fatalf("fields = %d, want 2", len(s.Fields))
	}
	if s.Fields[0].Path != "db.host" {
		t.Errorf("field[0].Path = %q, want db.host", s.Fields[0].Path)
	}
	if s.Fields[1].Path != "db.port" {
		t.Errorf("field[1].Path = %q, want db.port", s.Fields[1].Path)
	}
}
