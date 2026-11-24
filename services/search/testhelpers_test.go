package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/services/search/analytics"
)

// testRSAKey is generated once per test binary run and used to sign all test JWTs.
var testRSAKey, _ = rsa.GenerateKey(rand.Reader, 2048)

// testJWTKeyfunc is the jwt.Keyfunc that validates tokens signed with testRSAKey.
func testJWTKeyfunc(t *jwt.Token) (interface{}, error) {
	return &testRSAKey.PublicKey, nil
}

// makeToken returns a signed JWT with the given organizationId and userId claims.
func makeToken(orgID, userID string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"organizationId": orgID,
		"userId":         userID,
		"exp":            time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString(testRSAKey)
	if err != nil {
		panic("makeToken: " + err.Error())
	}
	return signed
}

// testOrgID is a valid UUID used as the org ID in handler tests.
const testOrgID = "00000000-0000-0000-0000-000000000001"

// testUserID is a stable user ID used alongside testOrgID in handler tests.
const testUserID = "00000000-0000-0000-0000-000000000002"

// testClient has an explicit timeout so tests fail fast rather than hanging.
var testClient = &http.Client{Timeout: 5 * time.Second}

// stubIntegrationsRepo is a configurable stub for IntegrationsRepository.
type stubIntegrationsRepo struct {
	integrations  []models.Integration
	integrErr     error
	createErr     error
	getErr        error
	upsertResult  models.Integration
	upsertCreated bool
	upsertErr     error
	upsertConfig  *models.IntegrationConfig
	upsertOrgID   *uuid.UUID
	updateResult  models.Integration
	updateErr     error
	updateCalls   int
	deleteResult  models.Integration
	deleteErr     error
}

func (s *stubIntegrationsRepo) CreateIntegration(_ context.Context, i models.Integration) (models.Integration, error) {
	if s.createErr != nil {
		return models.Integration{}, s.createErr
	}
	i.Key = uuid.New()
	i.State = models.IntegrationStateActive
	return i, nil
}
func (s *stubIntegrationsRepo) GetIntegration(_ context.Context, _, _ uuid.UUID) (models.Integration, error) {
	return models.Integration{}, s.getErr
}
func (s *stubIntegrationsRepo) ListIntegrations(_ context.Context, _ uuid.UUID) ([]models.Integration, error) {
	return s.integrations, s.integrErr
}
func (s *stubIntegrationsRepo) UpsertGitHubIntegration(_ context.Context, orgID uuid.UUID, config models.IntegrationConfig) (models.Integration, bool, error) {
	s.upsertOrgID = &orgID
	cfg := config
	s.upsertConfig = &cfg
	return s.upsertResult, s.upsertCreated, s.upsertErr
}
func (s *stubIntegrationsRepo) UpdateIntegration(_ context.Context, _, _ uuid.UUID, _ models.IntegrationPatch) (models.Integration, error) {
	s.updateCalls++
	return s.updateResult, s.updateErr
}
func (s *stubIntegrationsRepo) SoftDeleteIntegration(_ context.Context, _, _ uuid.UUID) (models.Integration, error) {
	return s.deleteResult, s.deleteErr
}

// stubScheduler is a configurable Scheduler for tests.
type stubScheduler struct {
	createErr  error
	createCall *models.Integration
}

func (s *stubScheduler) Create(_ context.Context, integration models.Integration) error {
	copied := integration
	s.createCall = &copied
	return s.createErr
}

// stubOrgRepo is a no-op stub for OrgRepository.
type stubOrgRepo struct{}

func (s *stubOrgRepo) CreateOrg(_ context.Context, _ uuid.UUID, _ string) error { return nil }

// stubValidator is a configurable stub for IntegrationValidator.
type stubValidator struct{ err error }

func (s *stubValidator) Validate(_ context.Context, _ models.Source, _ string) error { return s.err }

type stubGitHubAuth struct {
	startResp        githubStartResponse
	startErr         error
	startCall        *githubStartCall
	completeRedirect string
	completeErr      error
	completeQuery    url.Values
}

type githubStartCall struct {
	orgID    uuid.UUID
	userID   string
	returnTo string
}

func (s *stubGitHubAuth) Start(_ context.Context, orgID uuid.UUID, userID, returnTo string) (githubStartResponse, error) {
	s.startCall = &githubStartCall{orgID: orgID, userID: userID, returnTo: returnTo}
	return s.startResp, s.startErr
}

func (s *stubGitHubAuth) Complete(_ context.Context, query url.Values) (string, error) {
	s.completeQuery = query
	return s.completeRedirect, s.completeErr
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestServerFull(t, &stubIntegrationsRepo{}, &stubOrgRepo{}, &stubValidator{}, analytics.NewRegistry())
}

func newTestServerFull(t *testing.T, integrationsRepo IntegrationsRepository, orgRepo OrgRepository, validator IntegrationValidator, analyticsRegistry *analytics.Registry) *httptest.Server {
	return newTestServerWithGitHubAuthAndScheduler(t, integrationsRepo, orgRepo, validator, analyticsRegistry, &stubScheduler{}, nil)
}

func newTestServerWithGitHubAuth(t *testing.T, integrationsRepo IntegrationsRepository, orgRepo OrgRepository, validator IntegrationValidator, analyticsRegistry *analytics.Registry, githubAuth GitHubAuthService) *httptest.Server {
	return newTestServerWithGitHubAuthAndScheduler(t, integrationsRepo, orgRepo, validator, analyticsRegistry, &stubScheduler{}, githubAuth)
}

func newTestServerWithGitHubAuthAndScheduler(t *testing.T, integrationsRepo IntegrationsRepository, orgRepo OrgRepository, validator IntegrationValidator, analyticsRegistry *analytics.Registry, scheduler Scheduler, githubAuth GitHubAuthService) *httptest.Server {
	t.Helper()
	logger := zerolog.New(io.Discard)
	testApp := newAppWithGitHubAuth(loadConfig(), logger, integrationsRepo, orgRepo, validator, scheduler, testJWTKeyfunc, analyticsRegistry, githubAuth)
	return httptest.NewServer(testApp.router())
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func getWithToken(t *testing.T, url, orgID, userID string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+makeToken(orgID, userID))
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
