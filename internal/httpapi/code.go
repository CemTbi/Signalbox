// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/CemTbi/Signalbox/internal/modules/apis"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/pkg/codegen"
	"github.com/CemTbi/Signalbox/pkg/specload"
)

// getCode answers with code generated from a published version, as a zip.
// A version never changes, so the same version, target and package always
// give the same archive.
func (a *api) getCode(w http.ResponseWriter, r *http.Request, s *identity.Session) {
	versionID, ok := pathID(w, r, "versionId", id.Version, "version")
	if !ok {
		return
	}
	q := r.URL.Query()
	target, err := codegen.ParseTarget(q.Get("target"))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "invalid-target", "Choose what to generate: "+targetList()+".")
		return
	}
	pkg := q.Get("package")
	v, err := a.APIs.GetVersion(r.Context(), s.User.ID, versionID)
	if err != nil {
		a.apisError(w, r, "version", err)
		return
	}
	tag := `"` + id.Format(id.Revision, v.RevisionID) + "-" + string(target) + "-" + pkg + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	content, _, err := a.APIs.RevisionContent(r.Context(), s.User.ID, v.RevisionID)
	if err != nil {
		a.apisError(w, r, "version", err)
		return
	}

	// Generating takes a CPU for a moment; a few at a time keep the API
	// responsive.
	select {
	case a.codegenSlots <- struct{}{}:
		defer func() { <-a.codegenSlots }()
	case <-r.Context().Done():
		return
	}
	result, err := codegen.Generate(specload.Document{Data: []byte(content), Name: "openapi"}, codegen.Options{Target: target, Package: pkg})
	switch {
	case errors.Is(err, codegen.ErrInvalidPackage):
		writeProblem(w, r, http.StatusBadRequest, "invalid-package",
			"A package name is lowercase letters and digits, starting with a letter, and not a Go keyword. Leave it out to name the package after the API.")
		return
	case err != nil:
		writeProblem(w, r, http.StatusUnprocessableEntity, "codegen-failed",
			"The code couldn't be generated from this version: "+strings.TrimPrefix(err.Error(), "codegen: "))
		return
	}

	archive, err := zipResult(result, v)
	if err != nil {
		a.fail(w, r, "code", err)
		return
	}
	name := fmt.Sprintf("%s-%s-%s.zip", result.Package, target, v.Name)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Codegen-Warnings", fmt.Sprint(len(result.Warnings)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive)
}

// zipResult packs the generated files, and the warnings if there are any,
// stamped with the version's publication time so the archive is the same
// every time.
func zipResult(result *codegen.Result, v apis.Version) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(path string, content []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Deflate, Modified: v.PublishedAt.UTC()})
		if err != nil {
			return err
		}
		_, err = f.Write(content)
		return err
	}
	for _, f := range result.Files {
		if err := add(f.Path, f.Content); err != nil {
			return nil, err
		}
	}
	if len(result.Warnings) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "# What the generated code can't express\n\nThese parts of version %s aren't expressed in the generated code. Lines are in the version's document.\n\n", v.Name)
		for _, w := range result.Warnings {
			fmt.Fprintf(&b, "- Line %d, column %d: %s\n", w.Line, w.Column, w.Message)
		}
		if err := add(result.Package+"/WARNINGS.md", []byte(b.String())); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func targetList() string {
	ids := make([]string, len(codegen.Targets))
	for i, t := range codegen.Targets {
		ids[i] = string(t.ID)
	}
	return strings.Join(ids, ", ")
}
