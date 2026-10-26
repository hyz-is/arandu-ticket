//go:build kyse

package admin

import (
	"github.com/arandu-io/kyse/components"

	ticket "github.com/hyz-is/arandu-ticket"
)

@go
// ShowData is what the handler hands this page.
type ShowData = ticket.TeamShowPageData
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-6xl min-w-0 px-4 py-8 sm:px-6 lg:px-8">
	<p class="text-sm"><a class="text-muted-foreground hover:underline" href="{{ .BackURL }}">{{ .Labels.T("control.back") }}</a></p>

	<header class="mt-3 flex flex-wrap items-start justify-between gap-3">
		<div class="min-w-0">
			<p class="text-muted-foreground text-sm">{{ .Ticket.Code }} · {{ .Ticket.Space }}</p>
			<h1 class="text-2xl font-semibold tracking-tight break-words">{{ .Ticket.Subject }}</h1>
		</div>
		<div class="flex flex-wrap gap-2">
			{!! components.Badge(components.BadgeProps{Label: .Ticket.Priority, Variant: ticket.PriorityVariant(.Ticket.PriorityKey)}) !!}
			{!! components.Badge(components.BadgeProps{Label: .Ticket.Status, Variant: ticket.StatusVariant(.Ticket.StatusKey)}) !!}
		</div>
	</header>

	@if(.HasErrors())
		<ul class="text-destructive mt-4 grid gap-1 text-sm" role="alert">
			@foreach(.All() as line)
				<li>{{ line }}</li>
			@endforeach
		</ul>
	@endif

	<div class="mt-6 grid min-w-0 gap-6 lg:grid-cols-3">
		<div class="min-w-0 lg:col-span-2">
			<ol class="grid gap-4" aria-label="{{ .Labels.T("screen.conversation") }}">
				@foreach(.Messages as message)
					<li class="card min-w-0 p-4" data-role="{{ message.RoleKey }}" data-internal="{{ message.Internal }}">
						<div class="flex flex-wrap items-baseline justify-between gap-2 text-sm">
							<p class="font-semibold">{{ message.Author }} <span class="text-muted-foreground font-normal">· {{ message.Role }}</span>
								@if(message.Internal)
									{!! components.Badge(components.BadgeProps{Label: .Labels.T("screen.internal_note"), Variant: "secondary"}) !!}
								@endif
							</p>
							<time class="text-muted-foreground" datetime="{{ message.WhenISO }}">{{ message.When }}</time>
						</div>
						<div class="mt-2 text-sm break-words">{!! ticket.Prose(message) !!}</div>
						@if(len(message.Files) > 0)
							<ul class="mt-3 flex flex-wrap gap-2 text-sm" aria-label="{{ .Labels.T("field.files") }}">
								@foreach(message.Files as file)
									<li><a class="btn" data-variant="outline" data-size="sm" href="{{ file.URL }}">{{ file.Name }} <span class="text-muted-foreground">({{ file.Size }})</span></a></li>
								@endforeach
							</ul>
						@endif
					</li>
				@endforeach
			</ol>

			@if(.CanReply)
				<form class="card mt-6 grid gap-4 p-5" method="post" action="{{ .ReplyURL }}" enctype="multipart/form-data">
					@csrf
					<h2 class="text-base font-semibold">{{ .Labels.T("screen.team_reply_title") }}</h2>
					{!! components.Textarea(components.TextareaProps{Name: "body", Label: .Labels.T("field.body"), Hint: .Labels.T("screen.body_hint"), Rows: 6, Required: true, Page: .Form()}) !!}
					@if(.Files)
						<div class="grid gap-1.5">
							<label class="label" for="team-reply-files">{{ .Labels.T("field.files") }}</label>
							<input class="input" id="team-reply-files" type="file" name="files" multiple accept="{{ .Accept }}">
						</div>
					@endif
					<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.reply"), Type: "submit"}) !!}</div>
				</form>
			@endif

			@if(.CanNote)
				<form class="card mt-6 grid gap-4 p-5" method="post" action="{{ .NoteURL }}" enctype="multipart/form-data">
					@csrf
					<h2 class="text-base font-semibold">{{ .Labels.T("screen.note_title") }}</h2>
					<p class="text-muted-foreground -mt-2 text-sm">{{ .Labels.T("screen.note_lead") }}</p>
					{!! components.Textarea(components.TextareaProps{ID: "note-body", Name: "body", Label: .Labels.T("field.note"), Rows: 4, Required: true}) !!}
					@if(.Files)
						<div class="grid gap-1.5">
							<label class="label" for="team-note-files">{{ .Labels.T("field.files") }}</label>
							<input class="input" id="team-note-files" type="file" name="files" multiple accept="{{ .Accept }}">
						</div>
					@endif
					<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.note"), Variant: "outline", Type: "submit"}) !!}</div>
				</form>
			@endif
		</div>

		<aside class="grid min-w-0 content-start gap-4" aria-label="{{ .Labels.T("screen.details") }}">
			<dl class="card grid gap-2 p-4 text-sm">
				<div><dt class="text-muted-foreground">{{ .Labels.T("field.requester") }}</dt><dd class="break-words">{{ .Ticket.Requester }}</dd></div>
				@if(.Ticket.RequesterEmail != "")
					<div><dt class="text-muted-foreground">{{ .Labels.T("field.email") }}</dt><dd class="break-all">{{ .Ticket.RequesterEmail }}</dd></div>
				@endif
				<div><dt class="text-muted-foreground">{{ .Labels.T("field.space") }}</dt><dd class="break-all">{{ .Ticket.Space }}</dd></div>
				<div><dt class="text-muted-foreground">{{ .Labels.T("field.origin") }}</dt><dd>{{ .Ticket.Origin }}</dd></div>
				@if(.Ticket.Category != "")
					<div><dt class="text-muted-foreground">{{ .Labels.T("field.category") }}</dt><dd>{{ .Ticket.Category }}</dd></div>
				@endif
				<div><dt class="text-muted-foreground">{{ .Labels.T("field.created_at") }}</dt><dd>{{ .Ticket.Created }}</dd></div>
				<div><dt class="text-muted-foreground">{{ .Labels.T("field.updated_at") }}</dt><dd>{{ .Ticket.Updated }}</dd></div>
			</dl>

			@if(.CanManage)
				<form class="card grid gap-3 p-4" method="post" action="{{ .StatusURL }}">
					@csrf
					{!! components.Select(components.SelectProps{ID: "ticket-status", Name: "status", Label: .Labels.T("field.status"), Value: .Ticket.StatusKey, Options: .StatusOptions}) !!}
					<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.save"), Variant: "outline", Size: "sm", Type: "submit"}) !!}</div>
				</form>
				<form class="card grid gap-3 p-4" method="post" action="{{ .PriorityURL }}">
					@csrf
					{!! components.Select(components.SelectProps{ID: "ticket-priority", Name: "priority", Label: .Labels.T("field.priority"), Value: .Ticket.PriorityKey, Options: .PriorityOptions}) !!}
					<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.save"), Variant: "outline", Size: "sm", Type: "submit"}) !!}</div>
				</form>
			@endif
			@if(.CanAssign)
				<form class="card grid gap-3 p-4" method="post" action="{{ .AssignURL }}">
					@csrf
					{!! components.Select(components.SelectProps{ID: "ticket-assignee", Name: "assignee", Label: .Labels.T("field.assignee"), Value: .Ticket.AssigneeID, Options: .AssigneeOptions}) !!}
					<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.save"), Variant: "outline", Size: "sm", Type: "submit"}) !!}</div>
				</form>
			@endif
		</aside>
	</div>
</section>
@endsection
