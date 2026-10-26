//go:build kyse

package ticket

import (
	"github.com/arandu-io/kyse/components"

	ticket "github.com/hyz-is/arandu-ticket"
)

@go
// ShowData is what the handler hands this page.
type ShowData = ticket.ShowPageData
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-3xl min-w-0 px-4 py-8 sm:px-6 lg:px-8">
	<p class="text-sm"><a class="text-muted-foreground hover:underline" href="{{ .BackURL }}">{{ .Labels.T("control.back") }}</a></p>

	<header class="mt-3 flex flex-wrap items-start justify-between gap-3">
		<div class="min-w-0">
			<p class="text-muted-foreground text-sm">{{ .Ticket.Code }}</p>
			<h1 class="text-2xl font-semibold tracking-tight break-words">{{ .Ticket.Subject }}</h1>
		</div>
		{!! components.Badge(components.BadgeProps{Label: .Ticket.Status, Variant: ticket.StatusVariant(.Ticket.StatusKey)}) !!}
	</header>
	<dl class="text-muted-foreground mt-3 flex flex-wrap gap-x-6 gap-y-1 text-sm">
		<div class="flex gap-1.5"><dt>{{ .Labels.T("field.created_at") }}</dt><dd>{{ .Ticket.Created }}</dd></div>
		<div class="flex gap-1.5"><dt>{{ .Labels.T("field.updated_at") }}</dt><dd>{{ .Ticket.Updated }}</dd></div>
		@if(.Ticket.Category != "")
			<div class="flex gap-1.5"><dt>{{ .Labels.T("field.category") }}</dt><dd>{{ .Ticket.Category }}</dd></div>
		@endif
	</dl>

	<ol class="mt-6 grid gap-4" aria-label="{{ .Labels.T("screen.conversation") }}">
		@foreach(.Messages as message)
			<li class="card min-w-0 p-4" data-role="{{ message.RoleKey }}">
				<div class="flex flex-wrap items-baseline justify-between gap-2 text-sm">
					<p class="font-semibold">{{ message.Author }} <span class="text-muted-foreground font-normal">· {{ message.Role }}</span></p>
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
			<h2 class="text-base font-semibold">{{ .Labels.T("screen.reply_title") }}</h2>
			@if(.HasErrors())
				<ul class="text-destructive grid gap-1 text-sm" role="alert">
					@foreach(.All() as line)
						<li>{{ line }}</li>
					@endforeach
				</ul>
			@endif
			{!! components.Textarea(components.TextareaProps{Name: "body", Label: .Labels.T("field.body"), Hint: .Labels.T("screen.body_hint"), Rows: 6, Required: true, Page: .Form()}) !!}
			@if(.Files)
				<div class="grid gap-1.5">
					<label class="label" for="reply-files">{{ .Labels.T("field.files") }}</label>
					<input class="input" id="reply-files" type="file" name="files" multiple accept="{{ .Accept }}" aria-describedby="reply-files-hint">
					<p class="text-muted-foreground text-xs" id="reply-files-hint">{{ .FilesHint(.MaxFiles, .MaxMB) }}</p>
				</div>
			@endif
			<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.reply"), Type: "submit"}) !!}</div>
		</form>
	@endif

	<div class="mt-6 flex flex-wrap gap-2">
		@if(.CanClose)
			<form method="post" action="{{ .CloseURL }}">
				@csrf
				{!! components.Button(components.ButtonProps{Label: .Labels.T("control.close"), Variant: "outline", Type: "submit"}) !!}
			</form>
		@endif
		@if(.CanReopen)
			<form method="post" action="{{ .ReopenURL }}">
				@csrf
				{!! components.Button(components.ButtonProps{Label: .Labels.T("control.reopen"), Type: "submit"}) !!}
			</form>
		@endif
	</div>
</section>
@endsection
