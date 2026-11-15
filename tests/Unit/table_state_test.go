package unit_test

import (
	"testing"

	"github.com/arandu-io/kyse/components"

	ticket "github.com/hyz-is/arandu-ticket"
)

// TestTheCustomersTableStateKeepsWhatTheRequestAsked reads the list's state the
// way a data table does: the address of the next step carries the search, the
// statuses and the columns switched off, so a step never loses one of them.
func TestTheCustomersTableStateKeepsWhatTheRequestAsked(t *testing.T) {
	t.Parallel()

	state := ticket.IndexPageData{
		Query: "invoice", Statuses: []string{"open", "closed"}, SortKey: "number", SortDir: "asc",
		CurrentPage: 2, PerPage: 25, Hidden: []string{"category"},
	}.TableState()

	got := state.Values(components.DataTableChange{Page: 3})
	if got.Get("q") != "invoice" || got.Get("sort") != "number" || got.Get("dir") != "asc" || got.Get("page") != "3" {
		t.Errorf("the next page lost the search or the order: %v", got)
	}
	if status := got["status"]; len(status) != 2 {
		t.Errorf("the next page lost a status: %v", got)
	}
	if hide := got["hide"]; len(hide) != 1 || hide[0] != "category" {
		t.Errorf("the next page lost the hidden column: %v", got)
	}
	if got.Get("per_page") != "" {
		t.Errorf("the page size in force is the default and is left out: %v", got)
	}
}

// TestAListNobodyOrderedIsOrderedByTheLatestChange is the order the service
// applies when the request names none, and so the one the header shows.
func TestAListNobodyOrderedIsOrderedByTheLatestChange(t *testing.T) {
	t.Parallel()

	state := ticket.IndexPageData{PerPage: 25}.TableState()
	if state.SortKey() != "updated_at" || state.SortDir() != "desc" {
		t.Errorf("the default order is %q %q", state.SortKey(), state.SortDir())
	}
}

// TestTheTeamsTableStateCarriesTheAssigneeAndTheSpace keeps the two filters the
// team's list has beside its facets: the person the tickets are assigned to and
// the space they belong to.
func TestTheTeamsTableStateCarriesTheAssigneeAndTheSpace(t *testing.T) {
	t.Parallel()

	state := ticket.TeamIndexPageData{
		Priorities: []string{"urgent"}, Assignee: ticket.AssigneeNone, Space: "acme", PerPage: 25,
	}.TableState()

	got := state.Values(components.DataTableChange{Sort: "priority", Dir: "desc"})
	if got.Get("assignee") != ticket.AssigneeNone || got.Get("space") != "acme" || got.Get("priority") != "urgent" {
		t.Errorf("a new order lost a filter: %v", got)
	}
}
