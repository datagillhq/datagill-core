package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
)

type integrationResponse struct {
	Key    uuid.UUID                `json:"key"`
	Source models.Source            `json:"source"`
	Config models.IntegrationConfig `json:"config"`
	State  models.IntegrationState  `json:"state"`
}

func toIntegrationResponse(i models.Integration) integrationResponse {
	return integrationResponse{Key: i.Key, Source: i.Source, Config: i.Config, State: i.State}
}

func writeCode(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"code": code})
}

// providersHandler returns the list of supported integration providers.
func (a *app) providersHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]string{"providers": {string(models.SourceLaunchDarkly), string(models.SourceGitHub)}})
}

type createIntegrationRequest struct {
	Provider models.Source            `json:"source"`
	APIKey   string                   `json:"api_key"`
	Config   models.IntegrationConfig `json:"config"`
}

// createIntegrationHandler validates the API key, creates the org if needed, then creates the integration.
func (a *app) createIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID, err := uuid.Parse(orgIDFromCtx(r))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_ORG_ID")
		return
	}

	var req createIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST_BODY")
		return
	}

	if req.Provider == models.SourceGitHub {
		writeCode(w, http.StatusBadRequest, "INTEGRATION_USE_GITHUB_AUTH")
		return
	}

	// Check for duplicate provider on this org.
	existing, err := a.integrationsRepo.ListIntegrations(r.Context(), orgID)
	if err != nil {
		log.Error().Err(err).Msg("list integrations failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}
	for _, i := range existing {
		if i.Source == req.Provider {
			writeCode(w, http.StatusConflict, "INTEGRATION_DUPLICATE_PROVIDER")
			return
		}
	}

	if err := a.validator.Validate(r.Context(), req.Provider, req.APIKey); err != nil {
		if errors.Is(err, ErrInvalidAPIKey) {
			writeCode(w, http.StatusBadRequest, "INTEGRATION_INVALID_API_KEY")
			return
		}
		writeCode(w, http.StatusBadRequest, "INTEGRATION_PROVIDER_UNREACHABLE")
		return
	}

	if err := a.orgRepo.CreateOrg(r.Context(), orgID, orgID.String()); err != nil {
		log.Error().Err(err).Msg("create org failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	created, err := a.integrationsRepo.CreateIntegration(r.Context(), models.Integration{
		OrgID:  orgID,
		Source: req.Provider,
		APIKey: req.APIKey,
		Config: req.Config,
	})
	if err != nil {
		log.Error().Err(err).Msg("create integration failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	if err := a.scheduler.Create(r.Context(), created); err != nil {
		log.Error().Err(err).Str("integration_key", created.Key.String()).Msg("create schedule failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusCreated, toIntegrationResponse(created))
}

// listIntegrationsHandler returns all active and paused integrations for the org.
func (a *app) listIntegrationsHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID, err := uuid.Parse(orgIDFromCtx(r))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_ORG_ID")
		return
	}

	integrations, err := a.integrationsRepo.ListIntegrations(r.Context(), orgID)
	if err != nil {
		log.Error().Err(err).Msg("list integrations failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	resp := make([]integrationResponse, len(integrations))
	for i, v := range integrations {
		resp[i] = toIntegrationResponse(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": resp})
}

type startGitHubAuthRequest struct {
	ReturnTo string `json:"return_to"`
}

func (a *app) startGitHubAuthHandler(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(orgIDFromCtx(r))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_ORG_ID")
		return
	}

	var req startGitHubAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST_BODY")
		return
	}

	resp, err := a.githubAuth.Start(r.Context(), orgID, userIDFromCtx(r), req.ReturnTo)
	if err != nil {
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (a *app) githubAuthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	redirectURL, err := a.githubAuth.Complete(r.Context(), r.URL.Query())
	if err != nil {
		if redirectURL == "" {
			redirectURL = buildGitHubErrorRedirect(a.config.FrontendURL, "/settings/integrations", "integration_create_failed")
		}
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func buildGitHubErrorRedirect(frontendURL, returnTo, code string) string {
	base, err := url.Parse(frontendURL)
	if err != nil {
		return "/settings/integrations?provider=github&status=error&code=" + code
	}
	target, err := base.Parse(normalizeReturnTo(returnTo))
	if err != nil {
		return "/settings/integrations?provider=github&status=error&code=" + code
	}
	q := target.Query()
	q.Set("provider", "github")
	q.Set("status", "error")
	q.Set("code", code)
	target.RawQuery = q.Encode()
	return target.String()
}

type updateIntegrationRequest struct {
	APIKey *string                  `json:"api_key,omitempty"`
	State  *models.IntegrationState `json:"state,omitempty"`
}

// updateIntegrationHandler applies a partial update to an integration.
func (a *app) updateIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID, err := uuid.Parse(orgIDFromCtx(r))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_ORG_ID")
		return
	}
	key, err := uuid.Parse(chi.URLParam(r, "key"))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_INTEGRATION_KEY")
		return
	}

	var req updateIntegrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_REQUEST_BODY")
		return
	}

	if req.APIKey != nil {
		existing, err := a.integrationsRepo.GetIntegration(r.Context(), orgID, key)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				writeCode(w, http.StatusNotFound, "INTEGRATION_NOT_FOUND")
				return
			}
			log.Error().Err(err).Msg("get integration failed")
			writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
			return
		}
		if err := a.validator.Validate(r.Context(), existing.Source, *req.APIKey); err != nil {
			if errors.Is(err, ErrInvalidAPIKey) {
				writeCode(w, http.StatusBadRequest, "INTEGRATION_INVALID_API_KEY")
				return
			}
			writeCode(w, http.StatusBadRequest, "INTEGRATION_PROVIDER_UNREACHABLE")
			return
		}
	}

	updated, err := a.integrationsRepo.UpdateIntegration(r.Context(), orgID, key, models.IntegrationPatch{
		APIKey: req.APIKey,
		State:  req.State,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeCode(w, http.StatusNotFound, "INTEGRATION_NOT_FOUND")
			return
		}
		log.Error().Err(err).Msg("update integration failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusOK, toIntegrationResponse(updated))
}

// deleteIntegrationHandler soft-deletes an integration.
func (a *app) deleteIntegrationHandler(w http.ResponseWriter, r *http.Request) {
	log := zerolog.Ctx(r.Context())
	orgID, err := uuid.Parse(orgIDFromCtx(r))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_ORG_ID")
		return
	}
	key, err := uuid.Parse(chi.URLParam(r, "key"))
	if err != nil {
		writeCode(w, http.StatusBadRequest, "INVALID_INTEGRATION_KEY")
		return
	}

	deleted, err := a.integrationsRepo.SoftDeleteIntegration(r.Context(), orgID, key)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeCode(w, http.StatusNotFound, "INTEGRATION_NOT_FOUND")
			return
		}
		log.Error().Err(err).Msg("delete integration failed")
		writeCode(w, http.StatusInternalServerError, "INTERNAL_ERROR")
		return
	}

	writeJSON(w, http.StatusOK, toIntegrationResponse(deleted))
}
