// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/buildinfo"
	"github.com/CemTbi/Signalbox/internal/health"
	"github.com/CemTbi/Signalbox/internal/modules/identity"
	"github.com/CemTbi/Signalbox/internal/modules/workspaces"
	"github.com/CemTbi/Signalbox/internal/platform/ratelimit"
	"github.com/CemTbi/Signalbox/internal/roles"
)

// DefaultMaxSpecBytes bounds the size of a document sent for checking.
const DefaultMaxSpecBytes = 10 << 20

// Identity is what the API needs from the identity module.
type Identity interface {
	SetupRequired(ctx context.Context) (bool, error)
	CompleteSetup(ctx context.Context, in identity.SetupInput, client identity.Client) (*identity.NewSession, error)
	SignIn(ctx context.Context, email, password string, client identity.Client) (*identity.NewSession, error)
	Authenticate(ctx context.Context, token string) (*identity.Session, error)
	SignOut(ctx context.Context, token string) error
	CreateUserAndSignIn(ctx context.Context, in identity.UserInput, client identity.Client) (*identity.NewSession, error)
	UserByEmail(ctx context.Context, email string) (identity.User, bool, error)
	Users(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]identity.User, error)
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
	publicOrigin string

	setupByIP       *ratelimit.Limiter
	signInByIP      *ratelimit.Limiter
	signInByAccount *ratelimit.Limiter
	invitationsByIP *ratelimit.Limiter
}

// NewHandler returns the api role's HTTP handler.
func NewHandler(d Deps) http.Handler {
	if d.MaxSpecBytes <= 0 {
		d.MaxSpecBytes = DefaultMaxSpecBytes
	}
	if d.PublicURL == nil {
		d.PublicURL = &url.URL{Scheme: "http", Host: "localhost:8080"}
	}
	a := &api{
		Deps:            d,
		cookie:          newSessionCookie(d.PublicURL),
		publicOrigin:    origin(d.PublicURL),
		setupByIP:       ratelimit.New(5, 12*time.Second), // 5 at once, then 5 a minute
		signInByIP:      ratelimit.New(10, 6*time.Second), // 10 at once, then 10 a minute
		signInByAccount: ratelimit.New(5, 12*time.Second), // 5 at once, then 5 a minute
		invitationsByIP: ratelimit.New(20, 3*time.Second), // 20 at once, then 20 a minute
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
	mux.Handle("/api/v1/sessions/current", methods{
		http.MethodGet:    a.authenticated(a.getCurrentSession),
		http.MethodDelete: a.authenticated(a.deleteCurrentSession),
	})
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

	mux.HandleFunc("/api/", notFound)
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
