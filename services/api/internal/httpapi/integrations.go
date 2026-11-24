package httpapi

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/services/api/internal/integrations"
)

// integrationResponse is an integration as the API returns it. The API key is never
// included.
type integrationResponse struct {
	Key    uuid.UUID                `json:"key"`
	Source models.Source            `json:"source"`
	Config models.IntegrationConfig `json:"config"`
	State  models.IntegrationState  `json:"state"`
}

func toResponse(i models.Integration) integrationResponse {
	return integrationResponse{Key: i.Key, Source: i.Source, Config: i.Config, State: i.State}
}

type createIntegrationRequest struct {
	Source models.Source            `json:"source"`
	APIKey string                   `json:"api_key"`
	Config models.IntegrationConfig `json:"config"`
}

type updateIntegrationRequest struct {
	APIKey *string                  `json:"api_key,omitempty"`
	State  *models.IntegrationState `json:"state,omitempty"`
}

// listProviders serves GET /v1/integrations/providers.
func (s *server) listProviders(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]models.Source{"providers": s.Integrations.Providers()})
}

// listIntegrations serves GET /v1/integrations.
func (s *server) listIntegrations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Integrations.List(r.Context(), principalFrom(r.Context()).OrgID)
	if err != nil {
		writeInternal(w, r, err, "list integrations failed")
		return
	}
	resp := make([]integrationResponse, len(list))
	for i, integration := range list {
		resp[i] = toResponse(integration)
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": resp})
}

// createIntegration serves POST /v1/integrations.
func (s *server) createIntegration(w http.ResponseWriter, r *http.Request) {
	var req createIntegrationRequest
	if err := decodeBody(r, &req, false); err != nil {
		writeCode(w, http.StatusBadRequest, codeInvalidBody)
		return
	}

	created, err := s.Integrations.Create(r.Context(), principalFrom(r.Context()).OrgID, integrations.CreateInput{
		Source: req.Source,
		APIKey: req.APIKey,
		Config: req.Config,
	})
	if err != nil {
		writeIntegrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toResponse(created))
}

// updateIntegration serves PUT /v1/integrations/{key}.
func (s *server) updateIntegration(w http.ResponseWriter, r *http.Request) {
	key, ok := integrationKey(w, r)
	if !ok {
		return
	}
	var req updateIntegrationRequest
	if err := decodeBody(r, &req, false); err != nil {
		writeCode(w, http.StatusBadRequest, codeInvalidBody)
		return
	}

	updated, err := s.Integrations.Update(r.Context(), principalFrom(r.Context()).OrgID, key, models.IntegrationPatch{
		APIKey: req.APIKey,
		State:  req.State,
	})
	if err != nil {
		writeIntegrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(updated))
}

// deleteIntegration serves DELETE /v1/integrations/{key}.
func (s *server) deleteIntegration(w http.ResponseWriter, r *http.Request) {
	key, ok := integrationKey(w, r)
	if !ok {
		return
	}
	deleted, err := s.Integrations.Delete(r.Context(), principalFrom(r.Context()).OrgID, key)
	if err != nil {
		writeIntegrationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(deleted))
}

// integrationKey parses the {key} path parameter, writing a 400 if it isn't a UUID.
func integrationKey(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	key, err := uuid.Parse(chi.URLParam(r, "key"))
	if err != nil {
		writeCode(w, http.StatusBadRequest, codeInvalidKey)
		return uuid.Nil, false
	}
	return key, true
}

// writeIntegrationError maps an integrations.Service error to its response.
func writeIntegrationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, integrations.ErrNotFound):
		// Also returned for another org's integration, so keys can't be probed.
		writeCode(w, http.StatusNotFound, codeNotFound)
	case errors.Is(err, integrations.ErrDuplicateProvider):
		writeCode(w, http.StatusConflict, codeDuplicateProvider)
	case errors.Is(err, integrations.ErrUseGitHubApp):
		writeCode(w, http.StatusBadRequest, codeUseGitHubAuth)
	case errors.Is(err, integrations.ErrInvalidAPIKey):
		writeCode(w, http.StatusBadRequest, codeInvalidAPIKey)
	case errors.Is(err, integrations.ErrProviderUnreachable):
		writeCode(w, http.StatusBadRequest, codeUnreachable)
	case errors.Is(err, integrations.ErrInvalidState):
		writeCode(w, http.StatusBadRequest, codeInvalidState)
	default:
		writeInternal(w, r, err, "integration request failed")
	}
}
