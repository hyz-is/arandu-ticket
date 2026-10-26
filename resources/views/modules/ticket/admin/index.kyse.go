//go:build kyse

package admin

import (
	"github.com/arandu-io/kyse/components"

	ticket "github.com/hyz-is/arandu-ticket"
)

@go
// IndexData is what the handler hands this page.
type IndexData = ticket.TeamIndexPageData

func sortKey(data IndexData) string {
	if data.SortKey == "" {
		return "updated_at"
	}
	return data.SortKey
}

func sortDir(data IndexData) string {
	if data.SortDir == "asc" {
		return "asc"
	}
	return "desc"
}

// ticketsTable is the team's work: the tickets of every space, paged, filtered,
// searched and ordered by the server. Each row opens the conversation.
func ticketsTable(data IndexData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, t := range data.Rows {
		assignee := t.Assignee
		if assignee == "" {
			assignee = "—"
		}
		rows = append(rows, components.TableRow{Key: t.ID, Cells: []components.TableCell{
			{Text: t.Code},
			{HTML: components.Link(components.LinkProps{Label: t.Subject, URL: t.URL, Variant: "hover"})},
			{Text: t.Requester},
			{Text: t.Space},
			{HTML: components.Badge(components.BadgeProps{Label: t.Priority, Variant: ticket.PriorityVariant(t.PriorityKey)})},
			{HTML: components.Badge(components.BadgeProps{Label: t.Status, Variant: ticket.StatusVariant(t.StatusKey)})},
			{Text: assignee},
			{Text: t.Updated},
		}})
	}
	return components.DataTableProps{
		ID: "ticket-team-list", Label: data.Labels.T("screen.team_index_title"), URL: data.URL,
		Caption: data.Labels.T("screen.team_index_lead"), ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Query: data.Query, SearchName: "q", SearchPlaceholder: data.Labels.T("screen.team_search"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.team_index_title")),
		Filters: []components.DataTableFilter{
			{Key: "status", Label: data.Labels.T("field.status"), Options: data.StatusOptions, Selected: data.Statuses},
			{Key: "priority", Label: data.Labels.T("field.priority"), Options: data.PriorityOptions, Selected: data.Priorities},
			{Key: "assignee", Label: data.Labels.T("field.assignee"), Options: data.AssigneeOptions, Selected: oneOf(data.Assignee)},
		},
		SortKey: sortKey(data), SortDir: sortDir(data),
		Paginator: data.Pager,
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.number"), Key: "number", Sortable: true},
			{Label: data.Labels.T("field.subject"), Key: "subject", Sortable: true},
			{Label: data.Labels.T("field.requester"), Key: "requester", Sortable: true, Hideable: true},
			{Label: data.Labels.T("field.space"), Key: "tenant", Sortable: true, Hideable: true},
			{Label: data.Labels.T("field.priority"), Key: "priority", Sortable: true},
			{Label: data.Labels.T("field.status"), Key: "status", Sortable: true},
			{Label: data.Labels.T("field.assignee"), Key: "assignee", Sortable: true, Hideable: true},
			{Label: data.Labels.T("field.updated_at"), Key: "updated_at", Sortable: true},
		},
		Rows: rows,
		Empty: components.EmptyProps{
			Title: data.Labels.T("screen.team_index_empty"), Message: data.Labels.T("screen.team_index_empty_lead"),
		},
	}
}

func oneOf(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-7xl min-w-0 px-4 py-8 sm:px-6 lg:px-8">
	<header>
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.team_index_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.team_index_lead") }}</p>
	</header>

	<div class="mt-6 min-w-0">{!! components.DataTable(ticketsTable(.)) !!}</div>
</section>
@endsection
