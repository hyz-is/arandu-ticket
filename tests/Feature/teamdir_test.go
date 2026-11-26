package feature_test

import (
	"context"
	"testing"

	hedatabase "github.com/arandu-io/hesape/database"
	permission "github.com/hyz-is/arandu-permission"

	ticket "github.com/hyz-is/arandu-ticket"
	"github.com/hyz-is/arandu-ticket/teamdir"
)

func TestTheTeamIsTheMembersOfAPermissionGroupAndAnyOfThemCanTakeATicket(t *testing.T) {
	t.Parallel()
	w := installWith(t, nil)
	ctx := context.Background()

	perm, err := permission.New(permission.Config{
		Tenant: realm, Actions: append(permission.Actions(), ticket.TeamActions()...),
	}, w.db, w.sessions, nil)
	if err != nil {
		t.Fatalf("building arandu-permission: %v", err)
	}
	for _, migration := range perm.Migrations() {
		if err := migration.Up(ctx, hedatabase.ForMigrations(w.conn)); err != nil {
			t.Fatalf("applying %s: %v", migration.GetName(), err)
		}
	}
	if _, err := perm.Service().Bootstrap(ctx, realm, permission.BootstrapRequest{
		Slug: "administradores", Name: "Administradores", Members: []string{"team-1", "team-2", "gone"},
		Actions: ticket.TeamActions(),
	}); err != nil {
		t.Fatalf("creating the team's group: %v", err)
	}

	names := map[string]string{"team-1": "Rita", "team-2": "Davi"}
	dir, err := teamdir.New(teamdir.Config{
		Permission: perm.Service(), Realm: realm, Group: "administradores",
		Names: func(_ context.Context, id string) (ticket.Person, error) {
			if name, ok := names[id]; ok {
				return ticket.Person{ID: id, Name: name}, nil
			}
			return ticket.Person{}, ticket.ErrNoAccount
		},
	})
	if err != nil {
		t.Fatalf("building the directory: %v", err)
	}
	people, err := dir.Team(ctx)
	if err != nil || len(people) != 2 {
		t.Fatalf("the team is %+v, %v; want Rita and Davi, with the member nobody knows left out", people, err)
	}

	// The module with that directory lets Rita give a ticket to Davi, by name.
	module, err := ticket.New(ticket.Config{TeamTenant: realm, Directory: dir}, w.db, w.sessions)
	if err != nil {
		t.Fatal(err)
	}
	record := open(t, w, member("acme", "user-ana"), "mudar", "x")
	taken, err := module.Team().Assign(ctx, agent("team-1"), record.ID, "team-2")
	if err != nil || taken.AssigneeID != "team-2" || taken.AssigneeName != "Davi" {
		t.Fatalf("giving the ticket to another member: %+v, %v", taken, err)
	}
	if _, err := module.Team().Assign(ctx, agent("team-1"), record.ID, "stranger"); err == nil {
		t.Fatal("somebody outside the group was given a ticket")
	}
	listed, err := module.Team().Team(ctx, agent("team-1"))
	if err != nil || len(listed) != 2 {
		t.Fatalf("the screens list %+v, %v", listed, err)
	}

	// A wiring mistake fails where it is written.
	if _, err := teamdir.New(teamdir.Config{Realm: realm, Group: "x"}); err == nil {
		t.Error("a directory with no permission service was built")
	}
}
