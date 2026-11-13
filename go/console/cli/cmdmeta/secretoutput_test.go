package cmdmeta

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const soSchema = `{"type":"object","properties":{"value":{"type":"string"},` +
	`"rows":{"type":"array","items":{"$ref":"#/$defs/row"}}},` +
	`"$defs":{"row":{"type":"object","properties":{"number":{"type":"string"}}}}}`

func soCmd(ann map[string]string) *cobra.Command { return &cobra.Command{Use: "x", Annotations: ann} }

func TestGetSecretOutput(t *testing.T) {
	cases := []struct {
		name    string
		ann     map[string]string
		want    SecretOutput
		wantErr bool
	}{
		{"absent", nil, SecretOutput{}, false},
		{"all", map[string]string{KeySecretOutput: "*"}, SecretOutput{All: true}, false},
		{"fields", map[string]string{KeySecretOutput: `["a","b"]`}, SecretOutput{Fields: []string{"a", "b"}}, false},
		{"empty value", map[string]string{KeySecretOutput: ""}, SecretOutput{}, true},
		{"bare word", map[string]string{KeySecretOutput: "value"}, SecretOutput{}, true},
		{"empty list", map[string]string{KeySecretOutput: "[]"}, SecretOutput{}, true},
		{"empty name", map[string]string{KeySecretOutput: `[""]`}, SecretOutput{}, true},
		{"wrong type", map[string]string{KeySecretOutput: `{"a":1}`}, SecretOutput{}, true},
	}
	for _, c := range cases {
		got, err := GetSecretOutput(soCmd(c.ann))
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
			continue
		}
		if got.All != c.want.All || strings.Join(got.Fields, ",") != strings.Join(c.want.Fields, ",") {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
		if got.Declared() != (c.want.All || len(c.want.Fields) > 0) {
			t.Errorf("%s: Declared() wrong", c.name)
		}
	}
}

func TestCheckSecretOutput(t *testing.T) {
	with := func(secret string) *cobra.Command {
		return soCmd(map[string]string{KeyOutputSchema: soSchema, KeyOutputSchemaVersion: "1.0", KeySecretOutput: secret})
	}
	if err := CheckSecretOutput(with(`["value","number"]`)); err != nil {
		t.Errorf("declared fields, one in $defs: %v", err)
	}
	if err := CheckSecretOutput(with(EncodeSecretOutput("value"))); err != nil {
		t.Errorf("encoded: %v", err)
	}
	err := CheckSecretOutput(with(`["value","valeu"]`))
	if err == nil || !strings.Contains(err.Error(), "valeu") {
		t.Errorf("typo not named: %v", err)
	}
	if err := CheckSecretOutput(with("*")); err != nil {
		t.Errorf("whole output needs no field: %v", err)
	}
	if err := CheckSecretOutput(soCmd(map[string]string{KeySecretOutput: "*"})); err != nil {
		t.Errorf("whole output needs no schema: %v", err)
	}
	if err := CheckSecretOutput(soCmd(map[string]string{KeySecretOutput: `["value"]`})); err == nil {
		t.Error("fields with no schema should be refused")
	}
	if err := CheckSecretOutput(soCmd(nil)); err != nil {
		t.Errorf("no declaration: %v", err)
	}
	if EncodeSecretOutput() != SecretOutputAll {
		t.Error("no fields encodes as the whole output")
	}
}
