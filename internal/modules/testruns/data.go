// SPDX-License-Identifier: Apache-2.0

package testruns

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/CemTbi/Signalbox/internal/modules/client"
)

// MaxDataBytes bounds a data file.
const MaxDataBytes = 5 << 20

// Data file formats.
const (
	DataCSV  = "csv"
	DataJSON = "json"
)

// DataError says what's wrong with a data file, for the person who chose
// it.
type DataError struct{ Message string }

func (e *DataError) Error() string { return e.Message }

func dataError(format string, args ...any) error {
	return &DataError{Message: fmt.Sprintf(format, args...)}
}

// ParseData reads a data file's rows: CSV with a header row, or a JSON
// array of objects. CSV values are strings; JSON values keep their types,
// as pm.iterationData shows them. Numbers are json.Number, so {{name}}
// gets their digits exactly.
func ParseData(format string, content []byte) ([]map[string]any, error) {
	if len(content) > MaxDataBytes {
		return nil, dataError("The data file is larger than 5 MiB.")
	}
	content = bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	var rows []map[string]any
	var err error
	switch format {
	case DataCSV:
		rows, err = parseCSV(content)
	case DataJSON:
		rows, err = parseJSON(content)
	default:
		return nil, dataError("Use a CSV or JSON data file.")
	}
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, dataError("The data file has no rows.")
	}
	if len(rows) > client.MaxDataRows {
		return nil, dataError("The data file has %d rows; use at most %d.", len(rows), client.MaxDataRows)
	}
	return rows, nil
}

func parseCSV(content []byte) ([]map[string]any, error) {
	r := csv.NewReader(bytes.NewReader(content))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, dataError("The data file is empty.")
	}
	if err != nil {
		return nil, csvError(err)
	}
	for i, h := range header {
		header[i] = strings.TrimSpace(h)
		if header[i] == "" {
			return nil, dataError("Column %d of the CSV header has no name.", i+1)
		}
	}
	var rows []map[string]any
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, csvError(err)
		}
		if len(rows) >= client.MaxDataRows {
			return nil, dataError("The data file has more than %d rows.", client.MaxDataRows)
		}
		if len(rec) == 1 && rec[0] == "" {
			continue
		}
		if len(rec) > len(header) {
			line, _ := r.FieldPos(0)
			return nil, dataError("Line %d of the CSV has %d values, but the header names %d columns.", line, len(rec), len(header))
		}
		row := make(map[string]any, len(header))
		for i, h := range header {
			v := ""
			if i < len(rec) {
				v = rec[i]
			}
			row[h] = v
		}
		rows = append(rows, row)
	}
}

func csvError(err error) error {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return dataError("Line %d of the CSV can't be read: %v.", pe.Line, pe.Err)
	}
	return dataError("The CSV can't be read: %v.", err)
}

func parseJSON(content []byte) ([]map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var raw []json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, dataError("The data file isn't a JSON array of objects: %v.", err)
	}
	if len(raw) > client.MaxDataRows {
		return nil, dataError("The data file has %d rows; use at most %d.", len(raw), client.MaxDataRows)
	}
	rows := make([]map[string]any, 0, len(raw))
	for i, m := range raw {
		var row map[string]any
		d := json.NewDecoder(bytes.NewReader(m))
		d.UseNumber()
		if err := d.Decode(&row); err != nil || row == nil {
			return nil, dataError("Item %d of the data file isn't an object.", i+1)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
