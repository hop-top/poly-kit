package output

import (
	"fmt"
	"io"
	"reflect"
	"strings"
)

// textFormatter renders structured data as plain text in one of three
// styles: kv (key=value pairs), lines (TSV-style for shell pipelines),
// or paragraph (human-readable record blocks).
//
// Like csv + table, columns derive from the `table` struct tag — same
// single source of truth — so switching styles never changes which
// fields appear.
type textFormatter struct{}

func init() {
	Default.Register(textFormatter{})
}

const (
	textStyleKV        = "kv"
	textStyleLines     = "lines"
	textStyleParagraph = "paragraph"
)

func (textFormatter) Key() string          { return Text }
func (textFormatter) Extensions() []string { return []string{".txt"} }
func (textFormatter) Options() []OptionSpec {
	return []OptionSpec{
		{
			Name:    "style",
			Type:    OptEnum,
			Default: textStyleKV,
			Enum:    []string{textStyleKV, textStyleLines, textStyleParagraph},
			Usage:   "output style",
		},
		{
			Name:    "separator",
			Type:    OptString,
			Default: "=",
			Usage:   "kv separator (kv style only)",
		},
	}
}

func (textFormatter) Render(w io.Writer, data any, opts Options, cols []string) error {
	rv := reflect.ValueOf(data)

	var elemType reflect.Type
	var elems []reflect.Value
	if rv.Kind() == reflect.Slice {
		if rv.Len() == 0 {
			return nil
		}
		elemType = rv.Index(0).Type()
		elems = make([]reflect.Value, rv.Len())
		for i := range rv.Len() {
			e := rv.Index(i)
			if e.Kind() == reflect.Ptr {
				e = e.Elem()
			}
			elems[i] = e
		}
	} else {
		if rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				return nil
			}
			rv = rv.Elem()
		}
		elemType = rv.Type()
		elems = []reflect.Value{rv}
	}

	columns := tableColumns(elemType)
	if len(columns) == 0 {
		return nil
	}
	if len(cols) > 0 {
		filtered, err := filterColumns(columns, cols)
		if err != nil {
			return err
		}
		columns = filtered
	}

	style := opts.GetString("style")
	if style == "" {
		style = textStyleKV
	}

	switch style {
	case textStyleKV:
		return renderTextKV(w, columns, elems, opts.GetString("separator"))
	case textStyleLines:
		return renderTextLines(w, columns, elems)
	case textStyleParagraph:
		return renderTextParagraph(w, columns, elems)
	default:
		return fmt.Errorf("text formatter: unknown style %q", style)
	}
}

func renderTextKV(w io.Writer, columns []column, elems []reflect.Value, sep string) error {
	if sep == "" {
		sep = "="
	}
	for i, e := range elems {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		for _, c := range columns {
			line := fmt.Sprintf("%s%s%v\n", c.header, sep, e.Field(c.fieldIdx).Interface())
			if _, err := io.WriteString(w, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderTextLines(w io.Writer, columns []column, elems []reflect.Value) error {
	for _, e := range elems {
		fields := make([]string, len(columns))
		for i, c := range columns {
			fields[i] = fmt.Sprintf("%v", e.Field(c.fieldIdx).Interface())
		}
		if _, err := io.WriteString(w, strings.Join(fields, "\t")+"\n"); err != nil {
			return err
		}
	}
	return nil
}

func renderTextParagraph(w io.Writer, columns []column, elems []reflect.Value) error {
	for i, e := range elems {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		header := fmt.Sprintf("Record %d:\n", i+1)
		if _, err := io.WriteString(w, header); err != nil {
			return err
		}
		for _, c := range columns {
			line := fmt.Sprintf("  %s: %v\n", c.header, e.Field(c.fieldIdx).Interface())
			if _, err := io.WriteString(w, line); err != nil {
				return err
			}
		}
	}
	return nil
}
