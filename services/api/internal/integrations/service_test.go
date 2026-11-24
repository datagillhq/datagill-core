package integrations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/datagill/datagill-core/models"
	"github.com/datagill/datagill-core/repository"
)

var testOrgID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type stubRepo struct {
	existing     []models.Integration
	listErr      error
	getErr       error
	createErr    error
	created      *models.Integration
	upsertResult models.Integration
	upsertErr    error
	upsertConfig *models.IntegrationConfig
	updateResult models.Integration
	updateErr    error
	updateCalled bool
	deleteResult models.Integration
	deleteErr    error
	deletedKeys  []uuid.UUID
	getResult    models.Integration
}

func (s *stubRepo) CreateIntegration(_ context.Context, i models.Integration) (models.Integration, error) {
	if s.createErr != nil {
		return models.Integration{}, s.createErr
	}
	i.Key = uuid.New()
	i.State = models.IntegrationStateActive
	s.created = &i
	return i, nil
}

func (s *stubRepo) GetIntegration(context.Context, uuid.UUID, uuid.UUID) (models.Integration, error) {
	return s.getResult, s.getErr
}

func (s *stubRepo) ListIntegrations(context.Context, uuid.UUID) ([]models.Integration, error) {
	return s.existing, s.listErr
}

func (s *stubRepo) UpsertGitHubIntegration(_ context.Context, _ uuid.UUID, config models.IntegrationConfig) (models.Integration, bool, error) {
	s.upsertConfig = &config
	return s.upsertResult, true, s.upsertErr
}

func (s *stubRepo) UpdateIntegration(context.Context, uuid.UUID, uuid.UUID, models.IntegrationPatch) (models.Integration, error) {
	s.updateCalled = true
	return s.updateResult, s.updateErr
}

func (s *stubRepo) SoftDeleteIntegration(_ context.Context, _, key uuid.UUID) (models.Integration, error) {
	s.deletedKeys = append(s.deletedKeys, key)
	return s.deleteResult, s.deleteErr
}

type stubOrgs struct{ created []uuid.UUID }

func (s *stubOrgs) CreateOrg(_ context.Context, orgID uuid.UUID, _ string) error {
	s.created = append(s.created, orgID)
	return nil
}

type stubScheduler struct {
	err     error
	created []models.Integration
}

func (s *stubScheduler) Create(_ context.Context, i models.Integration) error {
	s.created = append(s.created, i)
	return s.err
}

type stubValidator struct{ err error }

func (s stubValidator) Validate(context.Context, string) error { return s.err }

func newService(repo *stubRepo, scheduler *stubScheduler, validatorErr error) (*Service, *stubOrgs) {
	orgs := &stubOrgs{}
	svc := NewService(repo, orgs, scheduler, map[models.Source]KeyValidator{
		models.SourceLaunchDarkly: stubValidator{err: validatorErr},
	})
	return svc, orgs
}

var ldInput = CreateInput{Source: models.SourceLaunchDarkly, APIKey: "key", Config: models.IntegrationConfig{EnvironmentKey: "production"}}

func TestProviders(t *testing.T) {
	svc, _ := newService(&stubRepo{}, &stubScheduler{}, nil)
	want := []models.Source{models.SourceLaunchDarkly, models.SourceGitHub}
	if got := svc.Providers(); !slices.Equal(got, want) {
		t.Fatalf("Providers() = %v, want %v", got, want)
	}
}

func TestCreate(t *testing.T) {
	t.Run("creates org, integration and schedules", func(t *testing.T) {
		repo, scheduler := &stubRepo{}, &stubScheduler{}
		svc, orgs := newService(repo, scheduler, nil)

		got, err := svc.Create(t.Context(), testOrgID, ldInput)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !slices.Equal(orgs.created, []uuid.UUID{testOrgID}) {
			t.Errorf("expected org %s to be created, got %v", testOrgID, orgs.created)
		}
		if repo.created == nil || repo.created.OrgID != testOrgID || repo.created.APIKey != "key" {
			t.Errorf("unexpected integration saved: %+v", repo.created)
		}
		if len(scheduler.created) != 1 || scheduler.created[0].Key != got.Key {
			t.Errorf("expected schedules for %s, got %v", got.Key, scheduler.created)
		}
	})

	errorCases := []struct {
		name         string
		input        CreateInput
		repo         *stubRepo
		validatorErr error
		schedulerErr error
		want         error
	}{
		{"github goes through the app", CreateInput{Source: models.SourceGitHub}, &stubRepo{}, nil, nil, ErrUseGitHubApp},
		{"duplicate provider", ldInput, &stubRepo{existing: []models.Integration{{Source: models.SourceLaunchDarkly}}}, nil, nil, ErrDuplicateProvider},
		{"invalid key", ldInput, &stubRepo{}, ErrInvalidAPIKey, nil, ErrInvalidAPIKey},
		{"provider unreachable", ldInput, &stubRepo{}, ErrProviderUnreachable, nil, ErrProviderUnreachable},
		{"unknown provider", CreateInput{Source: "posthog"}, &stubRepo{}, nil, nil, ErrProviderUnreachable},
		{"scheduler failure", ldInput, &stubRepo{}, nil, errTemporalDown, errTemporalDown},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newService(tt.repo, &stubScheduler{err: tt.schedulerErr}, tt.validatorErr)
			if _, err := svc.Create(t.Context(), testOrgID, tt.input); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

var errTemporalDown = errors.New("temporal unavailable")

func TestCreate_RollsBackWhenSchedulingFails(t *testing.T) {
	t.Run("soft-deletes the new integration", func(t *testing.T) {
		repo, scheduler := &stubRepo{}, &stubScheduler{err: errTemporalDown}
		svc, _ := newService(repo, scheduler, nil)

		if _, err := svc.Create(t.Context(), testOrgID, ldInput); !errors.Is(err, errTemporalDown) {
			t.Fatalf("expected scheduler error, got %v", err)
		}
		if repo.created == nil || !slices.Equal(repo.deletedKeys, []uuid.UUID{repo.created.Key}) {
			t.Fatalf("expected the new integration to be soft-deleted, got %v", repo.deletedKeys)
		}
	})

	t.Run("reports a failed rollback too", func(t *testing.T) {
		dbDown := errors.New("db unavailable")
		svc, _ := newService(&stubRepo{deleteErr: dbDown}, &stubScheduler{err: errTemporalDown}, nil)

		_, err := svc.Create(t.Context(), testOrgID, ldInput)
		if !errors.Is(err, errTemporalDown) || !errors.Is(err, dbDown) {
			t.Fatalf("expected both errors, got %v", err)
		}
	})
}

func TestConnectGitHub(t *testing.T) {
	integration := models.Integration{Key: uuid.New(), OrgID: testOrgID, Source: models.SourceGitHub}
	config := models.IntegrationConfig{InstallationID: 12345, AccountLogin: "acme"}

	t.Run("upserts and ensures schedules", func(t *testing.T) {
		repo, scheduler := &stubRepo{upsertResult: integration}, &stubScheduler{}
		svc, orgs := newService(repo, scheduler, nil)

		if _, err := svc.ConnectGitHub(t.Context(), testOrgID, config); err != nil {
			t.Fatalf("ConnectGitHub: %v", err)
		}
		if len(orgs.created) != 1 || repo.upsertConfig == nil || repo.upsertConfig.InstallationID != 12345 {
			t.Fatalf("expected org and upsert, got orgs=%v config=%+v", orgs.created, repo.upsertConfig)
		}
		if len(scheduler.created) != 1 || scheduler.created[0].Key != integration.Key {
			t.Fatalf("expected schedules for %s, got %v", integration.Key, scheduler.created)
		}
	})

	t.Run("returns scheduler errors", func(t *testing.T) {
		svc, _ := newService(&stubRepo{upsertResult: integration}, &stubScheduler{err: errTemporalDown}, nil)
		if _, err := svc.ConnectGitHub(t.Context(), testOrgID, config); !errors.Is(err, errTemporalDown) {
			t.Fatalf("expected scheduler error, got %v", err)
		}
	})
}

func TestUpdate(t *testing.T) {
	key := uuid.New()
	newKey := "new-key"

	t.Run("validates a new api key before saving", func(t *testing.T) {
		repo := &stubRepo{getResult: models.Integration{Source: models.SourceLaunchDarkly}}
		svc, _ := newService(repo, &stubScheduler{}, ErrInvalidAPIKey)

		_, err := svc.Update(t.Context(), testOrgID, key, models.IntegrationPatch{APIKey: &newKey})
		if !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("expected ErrInvalidAPIKey, got %v", err)
		}
		if repo.updateCalled {
			t.Fatal("update must not run when the key is rejected")
		}
	})

	t.Run("missing integration on key change", func(t *testing.T) {
		svc, _ := newService(&stubRepo{getErr: repository.ErrNotFound}, &stubScheduler{}, nil)
		if _, err := svc.Update(t.Context(), testOrgID, key, models.IntegrationPatch{APIKey: &newKey}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("missing integration on state change", func(t *testing.T) {
		paused := models.IntegrationStatePaused
		svc, _ := newService(&stubRepo{updateErr: repository.ErrNotFound}, &stubScheduler{}, nil)
		if _, err := svc.Update(t.Context(), testOrgID, key, models.IntegrationPatch{State: &paused}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestUpdate_State(t *testing.T) {
	for _, state := range []models.IntegrationState{"deleted", "archived", ""} {
		t.Run("rejects "+string(state), func(t *testing.T) {
			repo := &stubRepo{}
			svc, _ := newService(repo, &stubScheduler{}, nil)
			if _, err := svc.Update(t.Context(), testOrgID, uuid.New(), models.IntegrationPatch{State: &state}); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("expected ErrInvalidState, got %v", err)
			}
			if repo.updateCalled {
				t.Fatal("update must not run for an invalid state")
			}
		})
	}
	for _, state := range []models.IntegrationState{models.IntegrationStateActive, models.IntegrationStatePaused} {
		t.Run("accepts "+string(state), func(t *testing.T) {
			svc, _ := newService(&stubRepo{}, &stubScheduler{}, nil)
			if _, err := svc.Update(t.Context(), testOrgID, uuid.New(), models.IntegrationPatch{State: &state}); err != nil {
				t.Fatalf("Update: %v", err)
			}
		})
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc, _ := newService(&stubRepo{deleteErr: repository.ErrNotFound}, &stubScheduler{}, nil)
	if _, err := svc.Delete(t.Context(), testOrgID, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLaunchDarklyValidator(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusOK, nil},
		{http.StatusUnauthorized, ErrInvalidAPIKey},
		{http.StatusForbidden, ErrInvalidAPIKey},
		{http.StatusNotFound, ErrInvalidAPIKey},
		{http.StatusInternalServerError, ErrProviderUnreachable},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "api-key" {
					t.Errorf("expected api key in Authorization header, got %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			v := NewLaunchDarklyValidator(&http.Client{Timeout: 5 * time.Second})
			v.url = srv.URL
			if err := v.Validate(t.Context(), "api-key"); !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}
