package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/services/api/analytics"
	"github.com/datagill/datagill-core/services/api/internal/githubapp"
	"github.com/datagill/datagill-core/services/api/internal/integrations"
)

const (
	testOrgID       = "00000000-0000-0000-0000-000000000001"
	testUserID      = "00000000-0000-0000-0000-000000000002"
	testFrontendURL = "http://localhost:3001"
)

var (
	testRSAKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	testClient    = &http.Client{Timeout: 5 * time.Second}
)

func testKeyfunc(*jwt.Token) (any, error) { return &testRSAKey.PublicKey, nil }

func signToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(testRSAKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func validToken(t *testing.T) string {
	return signToken(t, jwt.MapClaims{
		"organizationId": testOrgID,
		"userId":         testUserID,
		"exp":            time.Now().Add(time.Hour).Unix(),
	})
}

// stubIntegrations records the last call and returns the configured result.
type stubIntegrations struct {
	result      models.Integration
	list        []models.Integration
	err         error
	orgID       uuid.UUID
	createInput *integrations.CreateInput
	patch       *models.IntegrationPatch
}

func (s *stubIntegrations) Providers() []models.Source {
	return []models.Source{models.SourceLaunchDarkly, models.SourceGitHub}
}

func (s *stubIntegrations) Create(_ context.Context, orgID uuid.UUID, in integrations.CreateInput) (models.Integration, error) {
	s.orgID, s.createInput = orgID, &in
	return s.result, s.err
}

func (s *stubIntegrations) List(_ context.Context, orgID uuid.UUID) ([]models.Integration, error) {
	s.orgID = orgID
	return s.list, s.err
}

func (s *stubIntegrations) Update(_ context.Context, orgID, _ uuid.UUID, patch models.IntegrationPatch) (models.Integration, error) {
	s.orgID, s.patch = orgID, &patch
	return s.result, s.err
}

func (s *stubIntegrations) Delete(_ context.Context, orgID, _ uuid.UUID) (models.Integration, error) {
	s.orgID = orgID
	return s.result, s.err
}

type stubGitHub struct {
	install     githubapp.Install
	startErr    error
	startOrgID  uuid.UUID
	startUserID string
	startReturn string
	returnTo    string
	completeErr error
	callback    githubapp.Callback
}

func (s *stubGitHub) Start(orgID uuid.UUID, userID, returnTo string) (githubapp.Install, error) {
	s.startOrgID, s.startUserID, s.startReturn = orgID, userID, returnTo
	return s.install, s.startErr
}

func (s *stubGitHub) Complete(_ context.Context, cb githubapp.Callback) (string, error) {
	s.callback = cb
	return s.returnTo, s.completeErr
}

type stubPlugin struct {
	metric string
	result any
	err    error
	query  analytics.Query
}

func (s *stubPlugin) Metric() string    { return s.metric }
func (s *stubPlugin) ResponseType() any { return struct{}{} }
func (s *stubPlugin) Execute(_ context.Context, q analytics.Query) (any, error) {
	s.query = q
	return s.result, s.err
}

// testDeps returns Deps wired to fresh stubs; tests override what they need.
func testDeps() Deps {
	return Deps{
		Logger:        zerolog.New(io.Discard),
		AllowedOrigin: "http://localhost:3001",
		FrontendURL:   testFrontendURL,
		Keyfunc:       testKeyfunc,
		Integrations:  &stubIntegrations{},
		GitHub:        &stubGitHub{},
		Analytics:     analytics.NewRegistry(),
		Metrics:       prometheus.NewRegistry(),
	}
}

func newTestServer(t *testing.T, deps Deps) *httptest.Server {
	t.Helper()
	handler, err := New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request. token may be empty for an unauthenticated request; body is
// JSON-encoded unless it is a string, which is sent as-is.
func do(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = bytes.NewReader([]byte(b))
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("expected status %d, got %d", want, resp.StatusCode)
	}
}

// assertCode checks the status and the "code" field of the JSON body.
func assertCode(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	assertStatus(t, resp, status)
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != code {
		t.Fatalf("expected code %q, got %q", code, body["code"])
	}
}

func decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}
