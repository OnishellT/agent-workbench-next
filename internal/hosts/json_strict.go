package hosts

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// decodeStrictJSON decodes exactly one JSON document with no unknown fields.
func decodeStrictJSON(src io.Reader, value any) error {
	decoder := json.NewDecoder(src)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("solo se permite un documento JSON")
	}
	return nil
}

// decodeStrictRequest bounds the body to limit bytes and decodes it strictly.
func decodeStrictRequest(w http.ResponseWriter, r *http.Request, limit int64, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return decodeStrictJSON(r.Body, value)
}
