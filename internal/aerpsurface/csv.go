package aerpsurface

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// CSVOutput is the --output value that prints the records, or groups, a read answered as CSV.
const CSVOutput = "csv"

// CSVResult prints a read's records as CSV in text mode (--output csv): one row per record or
// group, one column per field, a relation as its display name, numbers as the provider sent them.
// In JSON mode it is the result itself. A result that holds no records prints as JSON, like every
// other text-mode result.
type CSVResult struct{ Result interface{} }

// MarshalJSON keeps the JSON contract unchanged.
func (c CSVResult) MarshalJSON() ([]byte, error) { return json.Marshal(c.Result) }

// RawText is the CSV rendering.
func (c CSVResult) RawText() string {
	encoded, err := json.Marshal(c.Result)
	if err != nil {
		return fmt.Sprintf("could not encode the result: %v\n", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var generic interface{}
	if err := decoder.Decode(&generic); err != nil {
		return fmt.Sprintf("could not decode the result: %v\n", err)
	}
	rows, ok := recordsOf(generic)
	if !ok {
		indented, _ := json.MarshalIndent(generic, "", "  ")
		return string(indented) + "\n"
	}
	columns := columnsOf(rows)
	var out bytes.Buffer
	writer := csv.NewWriter(&out)
	_ = writer.Write(columns)
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, column := range columns {
			cells[i] = cellOf(row[column])
		}
		_ = writer.Write(cells)
	}
	writer.Flush()
	return out.String()
}

// recordsOf finds the records of a result: the result itself when it is a list of objects, or its
// data member (an execution outcome).
func recordsOf(value interface{}) ([]map[string]interface{}, bool) {
	switch v := value.(type) {
	case []interface{}:
		rows := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			row, ok := item.(map[string]interface{})
			if !ok {
				return nil, false
			}
			rows = append(rows, row)
		}
		return rows, true
	case map[string]interface{}:
		if data, ok := v["data"]; ok {
			return recordsOf(data)
		}
	}
	return nil, false
}

// columnsOf is every field the rows carry: id, display_name and name first, the rest by name.
func columnsOf(rows []map[string]interface{}) []string {
	seen := map[string]bool{}
	var rest []string
	for _, row := range rows {
		for key := range row {
			if !seen[key] {
				seen[key] = true
				rest = append(rest, key)
			}
		}
	}
	sort.Strings(rest)
	var columns []string
	for _, first := range []string{"id", "display_name", "name"} {
		if seen[first] {
			columns = append(columns, first)
		}
	}
	for _, key := range rest {
		if key != "id" && key != "display_name" && key != "name" {
			columns = append(columns, key)
		}
	}
	return columns
}

// cellOf is one value as CSV text: a relation or a date bucket as its display name, a list as JSON.
func cellOf(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	case map[string]interface{}:
		if name, ok := v["display_name"]; ok {
			return cellOf(name)
		}
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
