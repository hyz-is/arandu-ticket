// Package teamdir is a ticket.Directory for the platform team made of an
// arandu-permission group.
//
// The module's own Directory is optional and, left out, lets a member of the team
// give a ticket only to themselves. An application that keeps its team in a
// permission group -- the group of its administrators -- passes this instead, and
// every member of the group can be given a ticket and shows by name. The package
// is separate from the module's root so the root keeps no dependency on
// arandu-permission.
package teamdir

import (
	"context"
	"errors"
	"fmt"

	"github.com/arandu-io/framework/security"
	permission "github.com/hyz-is/arandu-permission"

	ticket "github.com/hyz-is/arandu-ticket"
)

// Names says who a user is: the application's own lookup of a person by
// identifier, in the realm the team signs in to. It answers ticket.ErrNoAccount
// for an identifier that is nobody's, and the member is then left out of the
// list.
type Names func(ctx context.Context, id string) (ticket.Person, error)

// Config is what the directory reads.
type Config struct {
	// Permission is the application's permission service.
	Permission *permission.PermissionService
	// Realm is the tenant the team signs in to: ticket.Config.TeamTenant.
	Realm string
	// Group is the slug of the group the team is made of.
	Group string
	// Names resolves a member's identifier to a person.
	Names Names
}

// Directory implements ticket.Directory over a permission group.
type Directory struct {
	cfg Config
}

var _ ticket.Directory = (*Directory)(nil)

// New returns the directory, or the reason it cannot be built.
func New(cfg Config) (*Directory, error) {
	switch {
	case cfg.Permission == nil:
		return nil, errors.New("teamdir: Config.Permission is nil: pass the application's permission service")
	case !security.ValidTenant(cfg.Realm):
		return nil, fmt.Errorf("teamdir: Config.Realm is %q, which cannot be a tenant: pass ticket.Config.TeamTenant", cfg.Realm)
	case cfg.Group == "":
		return nil, errors.New("teamdir: Config.Group is empty: name the slug of the group the team is made of")
	case cfg.Names == nil:
		return nil, errors.New("teamdir: Config.Names is nil: a member is shown by name, and the application knows the names")
	}
	return &Directory{cfg: cfg}, nil
}

// Person names a user through Config.Names.
func (d *Directory) Person(ctx context.Context, _, id string) (ticket.Person, error) {
	return d.cfg.Names(ctx, id)
}

// Team lists the members of the group, by name, in the order the group keeps
// them. The group is read as a subject of the realm that holds the two
// permission actions that read a group and nothing else; the module has already
// asked the team's policy before it calls this. A group that does not exist
// lists nobody.
func (d *Directory) Team(ctx context.Context) ([]ticket.Person, error) {
	reader := security.Subject{ID: "ticket:team-directory", Tenant: d.cfg.Realm, Verified: true,
		Actions: []security.Action{permission.PermissionList, permission.PermissionView}}
	page, err := d.cfg.Permission.ListGroups(ctx, reader, permission.GroupListQuery{Search: d.cfg.Group, Limit: permission.MaxPageSize})
	if err != nil {
		return nil, err
	}
	groupID := ""
	for _, g := range page.Items {
		if g.Slug == d.cfg.Group {
			groupID = g.ID
		}
	}
	if groupID == "" {
		return nil, nil
	}
	ids, err := d.cfg.Permission.MembersOf(ctx, reader, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]ticket.Person, 0, len(ids))
	for _, id := range ids {
		person, err := d.cfg.Names(ctx, id)
		if errors.Is(err, ticket.ErrNoAccount) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, person)
	}
	return out, nil
}
