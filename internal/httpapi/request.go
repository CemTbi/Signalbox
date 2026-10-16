// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/id"
	"github.com/CemTbi/Signalbox/internal/platform/validate"
)

// maxJSONBody bounds request bodies other than OpenAPI documents. Control
// requests are small; 64 KiB leaves plenty of room.
const maxJSONBody = 64 << 10

const (
	jsonMediaType       = "application/json"
	mergePatchMediaType = "application/merge-patch+json"
)

// decodeJSON reads a JSON object into dst, strictly: unknown fields, values
// of the wrong type and anything after the object are errors. When it
// returns false it has already written the problem response.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, mediaTypes ...string) bool {
	if len(mediaTypes) == 0 {
		mediaTypes = []string{jsonMediaType}
	}
	if !hasMediaType(r, mediaTypes) {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "unsupported-media-type",
			"Send the request body as "+strings.Join(mediaTypes, " or ")+".")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeDecodeError(w, r, err)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeProblem(w, r, http.StatusBadRequest, "invalid-json", "Send exactly one JSON object as the request body.")
		return false
	}
	return true
}

func hasMediaType(r *http.Request, allowed []string) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	for _, a := range allowed {
		if mt == a {
			return true
		}
	}
	return false
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		tooLarge  *http.MaxBytesError
		syntax    *json.SyntaxError
		wrongType *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &tooLarge):
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "body-too-large",
			fmt.Sprintf("The request body is larger than %d KiB.", maxJSONBody>>10))
	case errors.Is(err, io.EOF):
		writeProblem(w, r, http.StatusBadRequest, "invalid-json", "Send a JSON object as the request body.")
	case errors.Is(err, io.ErrUnexpectedEOF):
		writeProblem(w, r, http.StatusBadRequest, "invalid-json", "The JSON in the request body is cut off.")
	case errors.As(err, &syntax):
		writeProblem(w, r, http.StatusBadRequest, "invalid-json",
			fmt.Sprintf("The request body isn't valid JSON: %s, at byte %d.", syntax.Error(), syntax.Offset))
	case errors.As(err, &wrongType):
		var fields validate.Errors
		pointer := "/" + strings.ReplaceAll(wrongType.Field, ".", "/")
		if wrongType.Field == "" {
			pointer = ""
		}
		fields.Add(pointer, "type", "Use "+jsonTypeName(wrongType.Type)+" here.")
		writeInvalid(w, r, fields)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		name, _ := strconv.Unquote(strings.TrimPrefix(err.Error(), "json: unknown field "))
		var fields validate.Errors
		fields.Add("/"+name, "additionalProperties", "Remove this field; the request doesn't have it.")
		writeInvalid(w, r, fields)
	default:
		writeProblem(w, r, http.StatusBadRequest, "invalid-json", "The request body couldn't be read as JSON.")
	}
}

func jsonTypeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	default:
		return "an object"
	}
}

// required records a missing field and returns the field's value or "".
func required(errs *validate.Errors, pointer string, v *string) string {
	if v == nil {
		errs.Add(pointer, "required", "This field is required.")
		return ""
	}
	return *v
}

// mergePatch is a JSON Merge Patch (RFC 7396) body: the fields it sets, and
// null for fields it removes.
type mergePatch map[string]json.RawMessage

// decodeMergePatch reads a merge patch whose fields must all be in allowed.
func decodeMergePatch(w http.ResponseWriter, r *http.Request, allowed ...string) (mergePatch, bool) {
	var patch mergePatch
	if !decodeJSON(w, r, &patch, mergePatchMediaType, jsonMediaType) {
		return nil, false
	}
	var fields validate.Errors
	for key := range patch {
		if !contains(allowed, key) {
			fields.Add("/"+key, "additionalProperties", "Remove this field; it can't be changed.")
		}
	}
	if len(patch) == 0 {
		fields.Add("", "minProperties", "Send at least one field to change.")
	}
	if len(fields) > 0 {
		writeInvalid(w, r, fields)
		return nil, false
	}
	return patch, true
}

// str reads a string field of the patch. It returns nil when the field is
// absent. A null value is an error unless removable, in which case it reads
// as the empty string.
func (p mergePatch) str(errs *validate.Errors, field string, removable bool) *string {
	raw, ok := p[field]
	if !ok {
		return nil
	}
	if string(raw) == "null" {
		if removable {
			empty := ""
			return &empty
		}
		errs.Add("/"+field, "type", "This field can't be removed. Send a new value instead.")
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		errs.Add("/"+field, "type", "Use a string here.")
		return nil
	}
	return &s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// etag is the strong entity tag of a resource version.
func etag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// precondition reads If-Match. "*" or no header means no precondition. A
// tag that isn't one of ours can't match, and weak tags never match: If-Match
// uses strong comparison (RFC 9110, section 13.1.1).
func precondition(r *http.Request) workspaces.Precondition {
	header := strings.TrimSpace(r.Header.Get("If-Match"))
	if header == "" || header == "*" {
		return workspaces.Precondition{}
	}
	versions := []int64{-1} // matches nothing, so a header of foreign tags fails
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if !strings.HasPrefix(tag, `"`) || !strings.HasSuffix(tag, `"`) || len(tag) < 2 {
			continue
		}
		if v, err := strconv.ParseInt(tag[1:len(tag)-1], 10, 64); err == nil {
			versions = append(versions, v)
		}
	}
	return workspaces.Precondition{Versions: versions}
}

// cursor is the opaque position in a list that nextCursor hands out.
type cursor struct {
	ID string `json:"id"`
}

func nextCursor(prefix id.Prefix, last uuid.UUID) string {
	b, _ := json.Marshal(cursor{ID: id.Format(prefix, last)})
	return base64.RawURLEncoding.EncodeToString(b)
}

// pageRequest reads the limit and cursor parameters of a list of resources
// with the given ID prefix. When it returns false it has already written
// the problem response.
func pageRequest(w http.ResponseWriter, r *http.Request, prefix id.Prefix) (workspaces.PageRequest, bool) {
	q := r.URL.Query()
	req := workspaces.PageRequest{Limit: workspaces.DefaultLimit}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > workspaces.MaxLimit {
			writeProblem(w, r, http.StatusBadRequest, "invalid-parameter",
				fmt.Sprintf("limit: Use a whole number from 1 to %d.", workspaces.MaxLimit))
			return req, false
		}
		req.Limit = n
	}
	if q.Has("cursor") {
		after, ok := parseCursor(q.Get("cursor"), prefix)
		if !ok {
			writeProblem(w, r, http.StatusBadRequest, "invalid-cursor",
				"The cursor is invalid or belongs to another list. Start again without a cursor.")
			return req, false
		}
		req.After = after
	}
	return req, true
}

func parseCursor(s string, prefix id.Prefix) (uuid.UUID, bool) {
	if len(s) > 512 {
		return uuid.Nil, false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return uuid.Nil, false
	}
	var c cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return uuid.Nil, false
	}
	u, err := id.Parse(prefix, c.ID)
	return u, err == nil
}
