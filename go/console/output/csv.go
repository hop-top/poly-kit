package output

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
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

	eol := "\n"
	if opts.GetBool("crlf") {
		eol = "\r\n"
	}
	quoteAll := opts.GetBool("quote-all")

	if !opts.GetBool("no-header") {
		header := make([]string, len(columns))
		for i, c := range columns {
			header[i] = c.header
		}
		if err := writeCSVRow(w, header, delim[0], eol, quoteAll); err != nil {
			return err
		}
	}
	for _, e := range elems {
		row := make([]string, len(columns))
		for i, c := range columns {
			row[i] = fmt.Sprintf("%v", e.Field(c.fieldIdx).Interface())
		}
		if err := writeCSVRow(w, row, delim[0], eol, quoteAll); err != nil {
			return err
		}
	}
	return nil
}

// writeCSVRow emits one record terminated by eol.
//
// Encoding is hand-rolled rather than delegated to encoding/csv because that
// package cannot express the required behavior: with UseCRLF set it DROPS a
// lone CR outright and rewrites an embedded LF to CRLF, both of which mutate
// the caller's value with no error. It also contradicts itself — this
// package's former quote-all path preserved the CR that the default path
// discarded. RFC 4180 lists CR and LF as separate alternatives inside
// `escaped`, so a bare CR between quotes is legal, and the W3C CSV on the Web
// note is explicit that line endings within escaped cells are not normalised.
// So a field is quoted and its bytes pass through verbatim; the record
// terminator is the only place a CRLF is synthesised.
func writeCSVRow(w io.Writer, fields []string, delim rune, eol string, quoteAll bool) error {
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteRune(delim)
		}
		if !quoteAll && !csvFieldNeedsQuotes(f, delim) {
			b.WriteString(f)
			continue
		}
		b.WriteByte('"')
		// RFC 4180: an embedded quote is doubled. Everything else, CR and LF
		// included, is written through untouched.
		b.WriteString(strings.ReplaceAll(f, `"`, `""`))
		b.WriteByte('"')
	}
	b.WriteString(eol)
	_, err := io.WriteString(w, b.String())
	return err
}

// csvFieldNeedsQuotes reproduces encoding/csv's quoting rule, which is the
// one the other runtimes were written to match: quote iff the field contains
// the active delimiter, a double quote, LF or CR, or begins with a unicode
// space. Note the asymmetry — a LEADING space forces quoting, a trailing one
// does not. `\.` alone terminates a PostgreSQL COPY stream, so it is quoted
// defensively.
func csvFieldNeedsQuotes(field string, delim rune) bool {
	if field == "" {
		return false
	}
	if field == `\.` {
		return true
	}
	if strings.ContainsRune(field, delim) || strings.ContainsAny(field, "\"\r\n") {
		return true
	}
	r1, _ := utf8.DecodeRuneInString(field)
	return unicode.IsSpace(r1)
}
