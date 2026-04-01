package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/models"
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

// stubRepo lets each test configure exactly the data and errors it needs.
type stubRepo struct {
	searchResults []SearchResult
	searchErr     error
	flags         []FlagSummary
	flagsErr      error
	flagDetail    FlagDetail
	flagDetailErr error
	history       []ChangelogEntry
	historyErr    error
	timeline      []ChangelogEntry
	timelineErr   error
}

func (s *stubRepo) Search(_ context.Context, _ SearchRequest) ([]SearchResult, error) {
	return s.searchResults, s.searchErr
}
func (s *stubRepo) ListFlags(_ context.Context, _ string, _ FlagFilters) ([]FlagSummary, error) {
	return s.flags, s.flagsErr
}
func (s *stubRepo) GetFlag(_ context.Context, _, _ string) (FlagDetail, error) {
	return s.flagDetail, s.flagDetailErr
}
func (s *stubRepo) GetFlagHistory(_ context.Context, _, _ string, _ Pagination) ([]ChangelogEntry, error) {
	return s.history, s.historyErr
}
func (s *stubRepo) GetTimeline(_ context.Context, _ string, _ Pagination) ([]ChangelogEntry, error) {
	return s.timeline, s.timelineErr
}

// testOrgID is a valid UUID used as the org ID in handler tests.
const testOrgID = "00000000-0000-0000-0000-000000000001"

// testClient has an explicit timeout so tests fail fast rather than hanging.
var testClient = &http.Client{Timeout: 5 * time.Second}

// repoErr is a generic repository error for error-path tests.
var repoErr = errors.New("repository unavailable")

// stubIntegrationsRepo is a configurable stub for IntegrationsRepository.
type stubIntegrationsRepo struct {
	integrations []models.Integration
	integrErr    error
	createErr    error
	getErr       error
	updateResult models.Integration
	updateErr    error
	deleteResult models.Integration
	deleteErr    error
}

func (s *stubIntegrationsRepo) CreateOrg(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
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
func (s *stubIntegrationsRepo) UpdateIntegration(_ context.Context, _, _ uuid.UUID, _ models.IntegrationPatch) (models.Integration, error) {
	return s.updateResult, s.updateErr
}
func (s *stubIntegrationsRepo) SoftDeleteIntegration(_ context.Context, _, _ uuid.UUID) (models.Integration, error) {
	return s.deleteResult, s.deleteErr
}

// stubValidator is a configurable stub for IntegrationValidator.
type stubValidator struct{ err error }

func (s *stubValidator) Validate(_ context.Context, _ models.Source, _ string) error { return s.err }

func newTestServer(t *testing.T, repo SearchRepository) *httptest.Server {
	t.Helper()
	return newTestServerFull(t, repo, &stubIntegrationsRepo{}, &stubValidator{})
}

func newTestServerFull(t *testing.T, repo SearchRepository, integrations IntegrationsRepository, validator IntegrationValidator) *httptest.Server {
	t.Helper()
	logger := zerolog.New(io.Discard)
	testApp := newApp(loadConfig(), logger, repo, integrations, validator, testJWTKeyfunc)
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

// testUserID is a stable user ID used alongside testOrgID in handler tests.
const testUserID = "00000000-0000-0000-0000-000000000002"

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
