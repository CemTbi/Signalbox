// SPDX-License-Identifier: Apache-2.0

package specengine_test

import (
	"context"
	"testing"

	"github.com/CemTbi/Signalbox/pkg/specengine"
)

func TestReportCarriesTheDocumentsInfo(t *testing.T) {
	for name, tt := range map[string]struct {
		doc  string
		want *specengine.Info
	}{
		"YAML":                 {"openapi: 3.1.1\ninfo:\n  title: Orders API\n  version: 2.4.0\npaths: {}\n", &specengine.Info{Title: "Orders API", Version: "2.4.0"}},
		"JSON, Swagger 2.0":    {`{"swagger":"2.0","info":{"version":"1","title":"Legacy"},"paths":{}}`, &specengine.Info{Title: "Legacy", Version: "1"}},
		"no info":              {"openapi: 3.1.1\npaths: {}\n", &specengine.Info{}},
		"info isn't a mapping": {"openapi: 3.1.1\ninfo: [title]\npaths: {}\n", &specengine.Info{}},
		"unreadable":           {"hello: world\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := specengine.Check(context.Background(), []byte(tt.doc), specengine.Options{})
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.want == nil && r.Info != nil:
				t.Errorf("Info = %+v, want none for a document that can't be read", r.Info)
			case tt.want != nil && (r.Info == nil || *r.Info != *tt.want):
				t.Errorf("Info = %+v, want %+v", r.Info, tt.want)
			}
		})
	}
}
