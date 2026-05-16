package pkl

import (
	"context"
	"fmt"
	"log"

	"hop.top/kit/go/console/wizard"
	"hop.top/kit/go/core/config"
)

// WizardOpts configures RunWizard behavior.
type WizardOpts struct {
	ConfigOpts config.Options
	Scope      config.Scope
	DryRun     bool
	Headless   map[string]any     // pre-filled answers for CI
	WizardOpts []wizard.RunOption // pass-through to wizard.Run
}

// RunWizard loads a PKL schema, generates wizard steps, collects
// answers, resolves computed defaults, validates, and writes config.
func RunWizard(
	ctx context.Context, pklPath string, opts WizardOpts,
) error {
	schema, err := LoadSchema(pklPath)
	if err != nil {
		return fmt.Errorf("pkl: load schema: %w", err)
	}

	fields := prefillDefaults(schema.Fields, opts.ConfigOpts)
	modifiedSchema := &Schema{
		ModuleName: schema.ModuleName,
		Fields:     fields,
	}

	steps, err := WizardSteps(modifiedSchema)
	if err != nil {
		return fmt.Errorf("pkl: wizard steps: %w", err)
	}

	w, err := wizard.New(steps...)
	if err != nil {
		return fmt.Errorf("pkl: create wizard: %w", err)
	}

	w.SetOnComplete(func(results map[string]any) error {
		return writeConfig(ctx, pklPath, schema, results, opts)
	})

	if opts.DryRun {
		w.SetDryRun(true)
	}

	if opts.Headless != nil {
		_, err = wizard.RunHeadless(ctx, w, opts.Headless)
		return err
	}

	return wizard.Run(ctx, w, opts.WizardOpts...)
}

// prefillDefaults reads existing config values and overrides schema
// defaults so the wizard shows current values, not schema defaults.
func prefillDefaults(
	fields []FieldDef, cfgOpts config.Options,
) []FieldDef {
	out := make([]FieldDef, len(fields))
	copy(out, fields)

	for i := range out {
		val, err := config.Get(out[i].Path, cfgOpts)
		if err != nil {
			continue // keep schema default
		}
		out[i].Default = val
	}
	return out
}

// writeConfig resolves computed fields, validates, and writes each
// key to the config file.
func writeConfig(
	ctx context.Context,
	pklPath string,
	schema *Schema,
	results map[string]any,
	opts WizardOpts,
) error {
	resolved, err := Resolve(ctx, pklPath, results)
	resolveFailed := err != nil
	if resolveFailed {
		log.Printf(
			"pkl: resolve computed fields: %v (continuing with raw answers)",
			err,
		)
		resolved = results
	}

	for _, field := range schema.Fields {
		val, ok := resolved[field.Path]
		if !ok {
			val, ok = results[field.Path]
		}
		if !ok {
			continue // skipped by When condition
		}

		// If resolve failed and field is computed, skip it — we can't
		// produce a value without the pkl binary.
		if field.Computed && resolveFailed {
			continue
		}

		strVal := fmt.Sprintf("%v", val)

		if err := ValidateValue(schema, field.Path, strVal); err != nil {
			return fmt.Errorf(
				"pkl: validate %s=%q: %w", field.Path, strVal, err,
			)
		}

		if err := config.Set(
			field.Path, strVal, opts.Scope, opts.ConfigOpts,
		); err != nil {
			return fmt.Errorf(
				"pkl: write %s: %w", field.Path, err,
			)
		}
	}
	return nil
}
