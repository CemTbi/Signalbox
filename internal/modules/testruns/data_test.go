// SPDX-License-Identifier: Apache-2.0

package testruns_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/modules/testruns"
)

func TestParseData(t *testing.T) {
	rows, err := testruns.ParseData(testruns.DataCSV, []byte("\xef\xbb\xbfuser, qty\nada,2\n\n\"b,ob\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rows) != "[map[qty:2 user:ada] map[qty: user:b,ob]]" {
		t.Errorf("csv rows = %v", rows)
	}
	rows, err = testruns.ParseData(testruns.DataJSON, []byte(`[{"id": 12345678901234567, "name": "ada", "tags": ["a"]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := rows[0]["id"].(json.Number); !ok || n.String() != "12345678901234567" {
		t.Errorf("json id = %#v", rows[0]["id"])
	}

	for _, tt := range []struct{ format, content, want string }{
		{"xml", "<a/>", "CSV or JSON"},
		{testruns.DataCSV, "", "empty"},
		{testruns.DataCSV, "a,,b\n1,2,3\n", "Column 2"},
		{testruns.DataCSV, "a\n1,2\n", "Line 2"},
		{testruns.DataCSV, "a\n\"open\n", "can't be read"},
		{testruns.DataCSV, "a\n", "no rows"},
		{testruns.DataJSON, `{"a": 1}`, "array of objects"},
		{testruns.DataJSON, `[1]`, "Item 1"},
		{testruns.DataJSON, `[]`, "no rows"},
		{testruns.DataJSON, "[" + strings.Repeat(`{},`, 1000) + "{}]", "1001 rows"},
		{testruns.DataCSV, strings.Repeat("x", testruns.MaxDataBytes+1), "5 MiB"},
	} {
		_, err := testruns.ParseData(tt.format, []byte(tt.content))
		var de *testruns.DataError
		if !errors.As(err, &de) || !strings.Contains(de.Message, tt.want) {
			t.Errorf("%s %.20q: %v, want %q", tt.format, tt.content, err, tt.want)
		}
	}
}
