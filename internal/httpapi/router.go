// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"runtime"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/ratelimit"
	"github.com/CemTbi/Signalbox/internal/roles"
	"github.com/CemTbi/Signalbox/pkg/mockengine"
	"github.com/CemTbi/Signalbox/pkg/refdocs"
)

// DefaultMaxSpecBytes bounds the size of a document sent for checking.
const DefaultMaxSpecBytes = 10 << 20

// Identity is what the API needs from the identity module.
type Identity interface {
	SetupRequired(ctx context.Context) (bool, error)
	CompleteSetup(ctx context.Context, in identity.SetupInput, client identity.Client) (*identity.NewSession, error)
	SignIn(ctx context.Context, email, password string, client identity.Client) (*identity.NewSession, error)
	Authenticate(ctx context.Context, token string) (*identity.Session, error)
	AuthenticateAccessToken(ctx context.Context, token string) (*identity.Session, error)
	CreateAccessToken(ctx context.Context, userID uuid.UUID, name string, lifetime time.Duration) (*identity.NewAccessToken, error)
	ListAccessTokens(ctx context.Context, userID uuid.UUID) ([]identity.AccessToken, error)
	RevokeAccessToken(ctx context.Context, userID, tokenID uuid.UUID) error
	SignOut(ctx context.Context, token string) error
	CreateUserAndSignIn(ctx context.Context, in identity.UserInput, client identity.Client) (*identity.NewSession, error)
	UserByEmail(ctx context.Context, email string) (identity.User, bool, error)
	Users(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]identity.User, error)
	SSOName() (string, bool)
	StartSSO(ctx context.Context, returnPath string) (identity.StartedSSO, error)
	FinishSSO(ctx context.Context, state, code, browserToken string, client identity.Client) (*identity.NewSession, string, error)
}

// Workspaces is what the API needs from the workspaces module.
type Workspaces interface {
	ListWorkspaces(ctx context.Context, userID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[workspaces.Workspace], error)
	CreateWorkspace(ctx context.Context, userID uuid.UUID, in workspaces.WorkspaceInput) (workspaces.Workspace, error)
	GetWorkspace(ctx context.Context, userID, workspaceID uuid.UUID) (workspaces.Workspace, error)
	UpdateWorkspace(ctx context.Context, userID, workspaceID uuid.UUID, pre workspaces.Precondition, patch workspaces.WorkspacePatch) (workspaces.Workspace, error)
	DeleteWorkspace(ctx context.Context, userID, workspaceID uuid.UUID, pre workspaces.Precondition) error
	ListProjects(ctx context.Context, userID, workspaceID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[workspaces.Project], error)
	CreateProject(ctx context.Context, userID, workspaceID uuid.UUID, in workspaces.ProjectInput) (workspaces.Project, error)
	GetProject(ctx context.Context, userID, projectID uuid.UUID) (workspaces.Project, error)
	UpdateProject(ctx context.Context, userID, projectID uuid.UUID, pre workspaces.Precondition, patch workspaces.ProjectPatch) (workspaces.Project, error)
	DeleteProject(ctx context.Context, userID, projectID uuid.UUID, pre workspaces.Precondition) error
	ListEnvironments(ctx context.Context, userID, projectID uuid.UUID) ([]workspaces.Environment, error)
	GetEnvironment(ctx context.Context, userID, environmentID uuid.UUID) (workspaces.Environment, workspaces.Role, error)

	ListMembers(ctx context.Context, userID, workspaceID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[workspaces.Member], error)
	MemberRole(ctx context.Context, workspaceID, userID uuid.UUID) (workspaces.Role, bool, error)
	ChangeRole(ctx context.Context, actorID, workspaceID, memberID uuid.UUID, role workspaces.Role) (workspaces.Member, error)
	RemoveMember(ctx context.Context, actorID, workspaceID, memberID uuid.UUID) error
	Invite(ctx context.Context, actorID, workspaceID uuid.UUID, in workspaces.InvitationInput) (workspaces.NewInvitation, error)
	ListInvitations(ctx context.Context, actorID, workspaceID uuid.UUID, p workspaces.PageRequest) (workspaces.Page[workspaces.Invitation], error)
	RevokeInvitation(ctx context.Context, actorID, invitationID uuid.UUID) error
	LookupInvitation(ctx context.Context, token string) (workspaces.InvitationDetails, error)
	AcceptInvitation(ctx context.Context, token string, userID uuid.UUID, email string) (workspaces.Workspace, error)
}

// Deps is everything the api role's handlers need.
type Deps struct {
	Logger *slog.Logger
	Health *health.Checker
	Build  buildinfo.Info
	Roles  []roles.Name
	// UI serves the web app. Nil serves 404s outside /api.
	UI http.Handler
	// MaxSpecBytes bounds request bodies that carry an OpenAPI document.
	MaxSpecBytes int64

	Identity   Identity
	Workspaces Workspaces
	APIs       APIs
	// Mocks finds the documents hosted mocks serve.
	Mocks MockSources
	// Client sends requests and keeps their history.
	Client Client
	// Collections keeps saved requests.
	Collections Collections
	// Variables keeps environments' variables and secrets.
	Variables Variables
	// Imports reads Postman files and curl commands, and exports
	// collections.
	Imports Imports
	// Runs runs collections as test suites; nil leaves the routes out.
	Runs Runs
	// PublicURL is the address people open Signalbox at. It decides the
	// session cookie's name and flags, and the origin that unsafe requests
	// must come from. Default http://localhost:8080.
	PublicURL *url.URL
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header
	// is believed.
	TrustedProxies []netip.Prefix
}

// api holds the handlers' shared state.
type api struct {
	Deps
	cookie       sessionCookie
	ssoCookie    ssoCookie
	publicOrigin string

	setupByIP       *ratelimit.Limiter
	signInByIP      *ratelimit.Limiter
	signInByAccount *ratelimit.Limiter
	invitationsByIP *ratelimit.Limiter
	ssoByIP         *ratelimit.Limiter
	mocksByIP       *ratelimit.Limiter
	sendsByUser     *ratelimit.Limiter
	mockCache       *revisionCache[*mockengine.Mock]
	referenceCache  *revisionCache[*refdocs.Reference]
	// codegenSlots bounds how many code generations run at once.
	codegenSlots chan struct{}
}

// NewHandler returns the api role's HTTP handler.
func NewHandler(d Deps) http.Handler {
	if d.MaxSpecBytes <= 0 {
		d.MaxSpecBytes = DefaultMaxSpecBytes
	}
	if d.PublicURL == nil {
		d.PublicURL = &url.URL{Scheme: "http", Host: "localhost:8080"}
	}
	session := newSessionCookie(d.PublicURL)
	a := &api{
		Deps:            d,
		cookie:          session,
		ssoCookie:       newSSOCookie(session),
		publicOrigin:    origin(d.PublicURL),
		setupByIP:       ratelimit.New(5, 12*time.Second),        // 5 at once, then 5 a minute
		signInByIP:      ratelimit.New(10, 6*time.Second),        // 10 at once, then 10 a minute
		signInByAccount: ratelimit.New(5, 12*time.Second),        // 5 at once, then 5 a minute
		invitationsByIP: ratelimit.New(20, 3*time.Second),        // 20 at once, then 20 a minute
		ssoByIP:         ratelimit.New(20, 3*time.Second),        // 20 at once, then 20 a minute
		mocksByIP:       ratelimit.New(300, 10*time.Millisecond), // 300 at once, then 100 a second
		sendsByUser:     ratelimit.New(30, 100*time.Millisecond), // 30 at once, then 10 a second
		mockCache:       newRevisionCache(64, func(c string) (*mockengine.Mock, error) { return mockengine.New([]byte(c)) }),
		referenceCache:  newRevisionCache(64, func(c string) (*refdocs.Reference, error) { return refdocs.Build([]byte(c)) }),
		codegenSlots:    make(chan struct{}, max(2, runtime.GOMAXPROCS(0)/2)),
	}

	mux := http.NewServeMux()
	mux.Handle("/livez", methods{http.MethodGet: d.Health.LiveHandler()})
	mux.Handle("/readyz", methods{http.MethodGet: d.Health.ReadyHandler()})
	mux.Handle("/api/v1/meta", methods{http.MethodGet: metaHandler(d)})
	mux.Handle("/api/v1/spec-checks", methods{http.MethodPost: a.protect(specChecksHandler(d))})

	mux.Handle("/api/v1/setup", methods{
		http.MethodGet:  http.HandlerFunc(a.getSetup),
		http.MethodPost: http.HandlerFunc(a.completeSetup),
	})
	mux.Handle("/api/v1/sessions", methods{http.MethodPost: http.HandlerFunc(a.createSession)})
	mux.Handle("/api/v1/sign-in-options", methods{http.MethodGet: http.HandlerFunc(a.getSignInOptions)})
	mux.Handle("/api/v1/sso/start", methods{http.MethodGet: http.HandlerFunc(a.startSSO)})
	mux.Handle("/api/v1/sso/callback", methods{http.MethodGet: http.HandlerFunc(a.finishSSO)})
	mux.Handle("/api/v1/sessions/current", methods{
		http.MethodGet:    a.browserOnly(a.getCurrentSession),
		http.MethodDelete: a.browserOnly(a.deleteCurrentSession),
	})
	mux.Handle("/api/v1/tokens", methods{
		http.MethodGet:  a.browserOnly(a.listAccessTokens),
		http.MethodPost: a.browserOnly(a.createAccessToken),
	})
	mux.Handle("/api/v1/tokens/{tokenId}", methods{http.MethodDelete: a.browserOnly(a.revokeAccessToken)})
	mux.Handle("/api/v1/me", methods{http.MethodGet: a.authenticated(a.getMe)})

	mux.Handle("/api/v1/workspaces", methods{
		http.MethodGet:  a.authenticated(a.listWorkspaces),
		http.MethodPost: a.authenticated(a.createWorkspace),
	})
	mux.Handle("/api/v1/workspaces/{workspaceId}", methods{
		http.MethodGet:    a.authenticated(a.getWorkspace),
		http.MethodPatch:  a.authenticated(a.updateWorkspace),
		http.MethodDelete: a.authenticated(a.deleteWorkspace),
	})
	mux.Handle("/api/v1/workspaces/{workspaceId}/projects", methods{
		http.MethodGet:  a.authenticated(a.listProjects),
		http.MethodPost: a.authenticated(a.createProject),
	})
	mux.Handle("/api/v1/workspaces/{workspaceId}/members", methods{
		http.MethodGet: a.authenticated(a.listMembers),
	})
	mux.Handle("/api/v1/workspaces/{workspaceId}/members/{userId}", methods{
		http.MethodPatch:  a.authenticated(a.updateMember),
		http.MethodDelete: a.authenticated(a.removeMember),
	})
	mux.Handle("/api/v1/workspaces/{workspaceId}/invitations", methods{
		http.MethodGet:  a.authenticated(a.listInvitations),
		http.MethodPost: a.authenticated(a.createInvitation),
	})
	mux.Handle("/api/v1/invitations/{invitationId}", methods{http.MethodDelete: a.authenticated(a.revokeInvitation)})
	mux.Handle("/api/v1/invitations/lookup", methods{http.MethodPost: http.HandlerFunc(a.lookUpInvitation)})
	mux.Handle("/api/v1/invitations/accept", methods{http.MethodPost: a.authenticated(a.acceptInvitation)})
	mux.Handle("/api/v1/invitations/sign-up", methods{http.MethodPost: http.HandlerFunc(a.signUpWithInvitation)})
	mux.Handle("/api/v1/projects/{projectId}", methods{
		http.MethodGet:    a.authenticated(a.getProject),
		http.MethodPatch:  a.authenticated(a.updateProject),
		http.MethodDelete: a.authenticated(a.deleteProject),
	})

	mux.Handle("/api/v1/projects/{projectId}/environments", methods{http.MethodGet: a.authenticated(a.listEnvironments)})
	mux.Handle("/api/v1/environments/{environmentId}", methods{http.MethodGet: a.authenticated(a.getEnvironment)})
	mux.Handle("/api/v1/environments/{environmentId}/variables", methods{
		http.MethodGet: a.authenticated(a.getVariables),
		http.MethodPut: a.authenticated(a.putVariables),
	})
	mux.Handle("/api/v1/projects/{projectId}/canvas", methods{http.MethodPut: a.authenticated(a.placeServices)})
	mux.Handle("/api/v1/projects/{projectId}/send", methods{http.MethodPost: a.authenticated(a.sendRequest)})
	mux.Handle("/api/v1/projects/{projectId}/history", methods{http.MethodGet: a.authenticated(a.listHistory)})
	mux.Handle("/api/v1/history/{entryId}", methods{http.MethodGet: a.authenticated(a.getHistoryEntry)})
	mux.Handle("/api/v1/projects/{projectId}/collections", methods{
		http.MethodGet:  a.authenticated(a.listCollections),
		http.MethodPost: a.authenticated(a.createCollection),
	})
	mux.Handle("/api/v1/collections/{collectionId}", methods{
		http.MethodGet:    a.authenticated(a.getCollection),
		http.MethodPatch:  a.authenticated(a.updateCollection),
		http.MethodDelete: a.authenticated(a.deleteCollection),
	})
	mux.Handle("/api/v1/projects/{projectId}/collection-imports", methods{http.MethodPost: a.authenticated(a.importCollection)})
	mux.Handle("/api/v1/collections/{collectionId}/export", methods{http.MethodGet: a.authenticated(a.exportCollection)})
	mux.Handle("/api/v1/environments/{environmentId}/variable-imports", methods{http.MethodPost: a.authenticated(a.importVariables)})
	mux.Handle("/api/v1/curl-imports", methods{http.MethodPost: a.authenticated(a.importCurl)})
	mux.Handle("/api/v1/collections/{collectionId}/items", methods{
		http.MethodGet:  a.authenticated(a.listItems),
		http.MethodPost: a.authenticated(a.createItem),
	})
	if a.Runs != nil {
		mux.Handle("/api/v1/collections/{collectionId}/runs", methods{
			http.MethodGet:  a.authenticated(a.listRuns),
			http.MethodPost: a.authenticated(a.startRun),
		})
		mux.Handle("/api/v1/runs/{runId}", methods{http.MethodGet: a.authenticated(a.getRun)})
		mux.Handle("/api/v1/runs/{runId}/cancel", methods{http.MethodPost: a.authenticated(a.cancelRun)})
		mux.Handle("/api/v1/runs/{runId}/steps", methods{http.MethodGet: a.authenticated(a.listRunSteps)})
		mux.Handle("/api/v1/runs/{runId}/steps/{seq}", methods{http.MethodGet: a.authenticated(a.getRunStep)})
		mux.Handle("/api/v1/runs/{runId}/report", methods{http.MethodGet: a.authenticated(a.getRunReport)})
	}
	mux.Handle("/api/v1/items/{itemId}", methods{
		http.MethodGet:    a.authenticated(a.getItem),
		http.MethodPatch:  a.authenticated(a.updateItem),
		http.MethodDelete: a.authenticated(a.deleteItem),
	})
	mux.Handle("/api/v1/projects/{projectId}/services", methods{
		http.MethodGet:  a.authenticated(a.listServices),
		http.MethodPost: a.authenticated(a.createService),
	})
	mux.Handle("/api/v1/services/{serviceId}", methods{
		http.MethodGet:    a.authenticated(a.getService),
		http.MethodPatch:  a.authenticated(a.updateService),
		http.MethodDelete: a.authenticated(a.deleteService),
	})
	mux.Handle("/api/v1/services/{serviceId}/specs", methods{
		http.MethodGet:  a.authenticated(a.listSpecs),
		http.MethodPost: a.authenticated(a.createSpec),
	})
	mux.Handle("/api/v1/specs/{specId}", methods{
		http.MethodGet:    a.authenticated(a.getSpec),
		http.MethodPatch:  a.authenticated(a.updateSpec),
		http.MethodDelete: a.authenticated(a.deleteSpec),
	})
	mux.Handle("/api/v1/specs/{specId}/revisions", methods{
		http.MethodGet:  a.authenticated(a.listRevisions),
		http.MethodPost: a.authenticated(a.createRevision),
	})
	mux.Handle("/api/v1/specs/{specId}/versions", methods{
		http.MethodGet:  a.authenticated(a.listVersions),
		http.MethodPost: a.authenticated(a.publishVersion),
	})
	mux.Handle("/api/v1/versions/{versionId}", methods{http.MethodGet: a.authenticated(a.getVersion)})
	mux.Handle("/api/v1/versions/{versionId}/reference", methods{http.MethodGet: a.authenticated(a.getReference)})
	mux.Handle("/api/v1/versions/{versionId}/code", methods{http.MethodGet: a.authenticated(a.getCode)})
	mux.Handle("/api/v1/revisions/{revisionId}", methods{http.MethodGet: a.authenticated(a.getRevision)})
	mux.Handle("/api/v1/revisions/{revisionId}/content", methods{http.MethodGet: a.authenticated(a.getRevisionContent)})
	mux.Handle("/api/v1/revisions/{revisionId}/report", methods{http.MethodGet: a.authenticated(a.getRevisionReport)})

	mux.HandleFunc("/api/", notFound)
	if d.Mocks != nil {
		mux.HandleFunc("/mocks/{specId}/{version}", a.serveMock)
		mux.HandleFunc("/mocks/{specId}/{version}/{path...}", a.serveMock)
	}
	if d.UI != nil {
		mux.Handle("/", d.UI)
	} else {
		mux.HandleFunc("/", notFound)
	}
	return chain(mux,
		withRequestID,
		logRequests(d.Logger),
		recoverPanics(d.Logger),
		securityHeaders,
	)
}
