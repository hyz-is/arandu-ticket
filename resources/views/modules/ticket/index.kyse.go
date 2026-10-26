//go:build kyse

package ticket

import (
	"github.com/arandu-io/kyse/components"

	ticket "github.com/hyz-is/arandu-ticket"
)

@go
// IndexData is what the handler hands this page.
//
// It is an alias rather than a struct declared here, so the shape is written
// once, beside the code that fills it.
type IndexData = ticket.IndexPageData

// sortKey and sortDir are the ordering in force; a list nobody ordered is
// ordered by the latest change, newest first.
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

// ticketsTable is the customer's list, paged, filtered, searched and ordered by
// the server. Each row opens the conversation.
func ticketsTable(data IndexData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, t := range data.Rows {
		rows = append(rows, components.TableRow{Key: t.ID, Cells: []components.TableCell{
			{Text: t.Code},
			{HTML: components.Link(components.LinkProps{Label: t.Subject, URL: t.URL, Variant: "hover"})},
			{Text: t.Category},
			{HTML: components.Badge(components.BadgeProps{Label: t.Status, Variant: ticket.StatusVariant(t.StatusKey)})},
			{Text: t.Updated},
		}})
	}
	return components.DataTableProps{
		ID: "ticket-list", Label: data.Labels.T("screen.index_title"), URL: data.URL,
		Caption: data.Labels.T("screen.index_lead"), ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Query: data.Query, SearchName: "q", SearchPlaceholder: data.Labels.T("control.search_placeholder"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.index_title")),
		Filters: []components.DataTableFilter{
			{Key: "status", Label: data.Labels.T("field.status"), Options: data.StatusOptions, Selected: data.Statuses},
		},
		SortKey: sortKey(data), SortDir: sortDir(data),
		Paginator: data.Pager,
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.number"), Key: "number", Sortable: true},
			{Label: data.Labels.T("field.subject"), Key: "subject", Sortable: true},
			{Label: data.Labels.T("field.category"), Key: "category", Sortable: true, Hideable: true},
			{Label: data.Labels.T("field.status"), Key: "status", Sortable: true},
			{Label: data.Labels.T("field.updated_at"), Key: "updated_at", Sortable: true},
		},
		Rows: rows,
		Empty: components.EmptyProps{
			Title: data.Labels.T("screen.index_empty"), Message: data.Labels.T("screen.index_empty_lead"),
		},
	}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-5xl min-w-0 px-4 py-8 sm:px-6 lg:px-8">
	<header class="flex flex-wrap items-start justify-between gap-4">
		<div class="min-w-0">
			<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.index_title") }}</h1>
			<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.index_lead") }}</p>
		</div>
		@if(.CanCreate)
			<a class="btn" href="{{ .NewURL }}">{{ .Labels.T("control.new") }}</a>
		@endif
	</header>

	<div class="mt-6 min-w-0">{!! components.DataTable(ticketsTable(.)) !!}</div>
</section>
@endsection
