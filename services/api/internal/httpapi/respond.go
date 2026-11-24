package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/rs/zerolog"
)

// Error codes returned in {"code": "..."} bodies. The UI switches on these, so they are
// part of the API contract.
const (
	codeInternal          = "INTERNAL_ERROR"
	codeInvalidOrgID      = "INVALID_ORG_ID"
	codeInvalidBody       = "INVALID_REQUEST_BODY"
	codeInvalidKey        = "INVALID_INTEGRATION_KEY"
	codeNotFound          = "INTEGRATION_NOT_FOUND"
	codeDuplicateProvider = "INTEGRATION_DUPLICATE_PROVIDER"
	codeUseGitHubAuth     = "INTEGRATION_USE_GITHUB_AUTH"
	codeInvalidAPIKey     = "INTEGRATION_INVALID_API_KEY"
	codeUnreachable       = "INTEGRATION_PROVIDER_UNREACHABLE"
	codeInvalidState      = "INTEGRATION_INVALID_STATE"
)

const maxBodyBytes = 1 << 20

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent, so an encode error can't be reported to the client.
	_ = json.NewEncoder(w).Encode(payload)
}

func writeCode(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"code": code})
}

// writeInternal logs err once, here where it is handled, and hides it from the client.
func writeInternal(w http.ResponseWriter, r *http.Request, err error, msg string) {
	zerolog.Ctx(r.Context()).Error().Err(err).Msg(msg)
	writeCode(w, http.StatusInternalServerError, codeInternal)
}

// decodeBody decodes a JSON body into dst. An empty body is an error unless optional.
func decodeBody(r *http.Request, dst any, optional bool) error {
	err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(dst)
	if optional && errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	return nil
}
