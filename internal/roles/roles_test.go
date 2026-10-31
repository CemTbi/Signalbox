// SPDX-License-Identifier: Apache-2.0

package roles_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/CemTbi/Signalbox/internal/roles"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    []roles.Name
		wantErr string
	}{
		{"all", []roles.Name{roles.API, roles.Runner, roles.Worker, roles.Ingest}, ""},
		{"", []roles.Name{roles.API, roles.Runner, roles.Worker, roles.Ingest}, ""},
		{"ingest", []roles.Name{roles.Ingest}, ""},
		{"runner,api", []roles.Name{roles.API, roles.Runner}, ""},
		{" API , api ", []roles.Name{roles.API}, ""},
		{"mock", nil, "planned but not available"},
		{"frontend", nil, "unknown role"},
		{",", nil, "no roles given"},
	}
	for _, tt := range tests {
		got, err := roles.Parse(tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse(%q) error = %v, want one containing %q", tt.in, err, tt.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
}

func TestEveryRoleIsKnown(t *testing.T) {
	if len(roles.All) != 6 {
		t.Fatalf("the architecture defines six roles, All has %d", len(roles.All))
	}
	if !roles.Available(roles.API) {
		t.Error("the api role must be available")
	}
}
