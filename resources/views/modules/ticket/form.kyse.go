//go:build kyse

package ticket

import (
	"github.com/arandu-io/kyse/components"

	ticket "github.com/hyz-is/arandu-ticket"
)

@go
// NewData is what the handler hands this page.
type NewData = ticket.NewPageData
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-2xl min-w-0 px-4 py-8 sm:px-6 lg:px-8">
	<header>
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.new_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.new_lead") }}</p>
	</header>

	<form class="card mt-6 grid gap-4 p-5" method="post" action="{{ .Action }}" enctype="multipart/form-data">
		@csrf
		@if(.HasErrors())
			<ul class="text-destructive grid gap-1 text-sm" role="alert">
				@foreach(.All() as line)
					<li>{{ line }}</li>
				@endforeach
			</ul>
		@endif
		{!! components.Field(components.FieldProps{Name: "subject", Label: .Labels.T("field.subject"), Required: true, Autofocus: true, Page: .Form()}) !!}
		{!! components.Field(components.FieldProps{Name: "category", Label: .Labels.T("field.category"), Hint: .Labels.T("screen.category_hint"), Page: .Form()}) !!}
		{!! components.Textarea(components.TextareaProps{Name: "body", Label: .Labels.T("field.body"), Hint: .Labels.T("screen.body_hint"), Rows: 8, Required: true, Page: .Form()}) !!}
		@if(.Files)
			<div class="grid gap-1.5">
				<label class="label" for="ticket-files">{{ .Labels.T("field.files") }}</label>
				<input class="input" id="ticket-files" type="file" name="files" multiple accept="{{ .Accept }}" aria-describedby="ticket-files-hint">
				<p class="text-muted-foreground text-xs" id="ticket-files-hint">{{ .FilesHint(.MaxFiles, .MaxMB) }}</p>
			</div>
		@endif
		<div class="flex flex-wrap items-center gap-2">
			{!! components.Button(components.ButtonProps{Label: .Labels.T("control.send"), Type: "submit"}) !!}
			<a class="btn" data-variant="outline" href="{{ .BackURL }}">{{ .Labels.T("control.cancel") }}</a>
		</div>
	</form>
</section>
@endsection
