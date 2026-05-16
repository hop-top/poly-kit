package output

import (
	"encoding/csv"
	"fmt"
	"io"
	"reflect"
)

// csvFormatter renders structured data as CSV using `table` tags as the
// single source of truth for column headers and field selection. This
// keeps CSV output in lock-step with the table formatter — switching
// between them never changes which fields appear.
type csvFormatter struct{}

func init() {
	Default.Register(csvFormatter{})
}

func (csvFormatter) Key() string          { return CSV }
func (csvFormatter) Extensions() []string { return []string{".csv"} }
func (csvFormatter) Options() []OptionSpec {
	return []OptionSpec{
		{
			Name:    "delimiter",
			Type:    OptString,
			Default: ",",
			Usage:   "field delimiter",
		},
		{
			Name:    "no-header",
			Type:    OptBool,
			Default: false,
			Usage:   "omit header row",
		},
		{
			Name:    "quote-all",
			Type:    OptBool,
			Default: false,
			Usage:   "quote every field, not just those needing it",
		},
		{
			Name:    "crlf",
			Type:    OptBool,
			Default: false,
			Usage:   "use CRLF line endings (default LF)",
		},
	}
}

func (csvFormatter) Render(w io.Writer, data any, opts Options, cols []string) error {
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
		elemType = rv.Type()
		if rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				return nil
			}
			rv = rv.Elem()
			elemType = rv.Type()
		}
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

	delim := []rune(opts.GetString("delimiter"))
	if len(delim) != 1 {
		return fmt.Errorf("option %q: delimiter must be exactly one character", "delimiter")
	}

	cw := csv.NewWriter(w)
	cw.Comma = delim[0]
	cw.UseCRLF = opts.GetBool("crlf")

	if opts.GetBool("quote-all") {
		// encoding/csv has no quote-all toggle; do it manually with a
		// pre-quoted pass-through writer below.
		return renderCSVQuoteAll(w, columns, elems, cw.Comma, cw.UseCRLF, opts.GetBool("no-header"))
	}

	if !opts.GetBool("no-header") {
		header := make([]string, len(columns))
		for i, c := range columns {
			header[i] = c.header
		}
		if err := cw.Write(header); err != nil {
			return err
		}
	}
	for _, e := range elems {
		row := make([]string, len(columns))
		for i, c := range columns {
			row[i] = fmt.Sprintf("%v", e.Field(c.fieldIdx).Interface())
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// renderCSVQuoteAll writes every field wrapped in double quotes,
// regardless of whether encoding/csv would otherwise quote it.
// Internal quotes are doubled per RFC 4180.
func renderCSVQuoteAll(
	w io.Writer,
	columns []column,
	elems []reflect.Value,
	delim rune,
	useCRLF bool,
	noHeader bool,
) error {
	eol := "\n"
	if useCRLF {
		eol = "\r\n"
	}
	writeRow := func(fields []string) error {
		for i, f := range fields {
			if i > 0 {
				if _, err := io.WriteString(w, string(delim)); err != nil {
					return err
				}
			}
			escaped := ""
			for _, r := range f {
				if r == '"' {
					escaped += `""`
					continue
				}
				escaped += string(r)
			}
			if _, err := io.WriteString(w, `"`+escaped+`"`); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, eol)
		return err
	}

	if !noHeader {
		header := make([]string, len(columns))
		for i, c := range columns {
			header[i] = c.header
		}
		if err := writeRow(header); err != nil {
			return err
		}
	}
	for _, e := range elems {
		row := make([]string, len(columns))
		for i, c := range columns {
			row[i] = fmt.Sprintf("%v", e.Field(c.fieldIdx).Interface())
		}
		if err := writeRow(row); err != nil {
			return err
		}
	}
	return nil
}
