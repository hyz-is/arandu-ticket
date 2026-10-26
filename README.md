<p align="center">
  <img src=".github/logo.svg" alt="Arandu" width="180">
</p>

# Arandu Ticket

Internal support tickets for a multi-tenant [Arandu](https://github.com/arandu-io/framework)
application: a conversation between a user of a space and the platform team
that answers it.

There are two sides, and they are separate. The customer lists their own
tickets, opens one, reads the conversation, replies, closes and reopens. The
team lists every space's tickets, reads the conversation with its internal
notes, answers, writes notes the customer never sees, and changes a ticket's
status, priority and assignee. Each side has its own controllers, its own
screens and its own prefix, and the application mounts each where its own
session is valid: the team's session only on the operator console, the
customer's only on the panel.

The module sends no e-mail, knows no table of users and keeps no list of the
team. Those are three small interfaces the application implements with what it
already has.

## Install

```bash
go get github.com/hyz-is/arandu-ticket
```

It requires [arandu-activitylog](https://github.com/hyz-is/arandu-activitylog)
and [arandu-markdown](https://github.com/hyz-is/arandu-markdown), and expects
the application to administer permissions with
[arandu-permission](https://github.com/hyz-is/arandu-permission).

## Wire it

An Arandu application registers a module explicitly. There is no service
provider, no container and no discovery, so these are the lines to paste into
`bootstrap/app.go` and there are no others.

The import, with the other module imports:

```go
import (
	ticket "github.com/hyz-is/arandu-ticket"
)
```

The construction, in `Build`, after the session store, the activity log and the
markdown module exist and before `k.Register`:

```go
	ticketModule, err := ticket.New(ticket.Config{
		// The tenant the platform team signs in to: the operator console's
		// realm. The one required setting.
		TeamTenant: cfg.Auth.Tenant,

		// Two sides, two prefixes. The application's own addresses, in the
		// segments of its language.
		Prefix:     "/painel/chamados",
		TeamPrefix: "/admin/chamados",
		Paths:      ticket.PortuguesePaths,

		Accounts:  accountResolver{users: users}, // e-mail -> user and space
		Directory: teamDirectory{users: users},   // names, and the team to assign to
		Notifier:  ticketMail{mailer: mailer},    // the application's own mailer

		Activity: activityModule.Logger(), // arandu-activitylog
		Markdown: markdownModule,          // arandu-markdown
		Files:    filesDisk,               // the private Disk: Cloudflare R2 in production

		// The actions arandu-permission fills are resolved on each request,
		// so its Roles() runs on every route; the console's guard runs on the
		// team's alone.
		Middleware:     []func(http.Handler) http.Handler{permissionModule.Roles()},
		TeamMiddleware: []func(http.Handler) http.Handler{consoleGuard},
	}, db, sessions)
	if err != nil {
		return App{}, err
	}
```

And the registration, inside the `k.Register(...)` call already there:

```go
		ticketModule,
```

An application that keeps the two sides on different routers mounts them
itself, each with its own guards, instead of registering the routes through
`k.Register`:

```go
ticketModule.CustomerRoutes(panel)  // under Prefix, with the panel's guards
ticketModule.TeamRoutes(console)    // under TeamPrefix, with the console's
```

Then, once, before the application serves:

```bash
aru migrate
```

The package owns four tables (`tickets`, `ticket_messages`,
`ticket_attachments`, `ticket_counters`), which is why `arandu.mod.toml` says
`migrations = true`. arandu-activitylog's migration runs in the same step.
Nothing migrates at boot.

## Give people access

The policies are in `policy.go`, and the actions reach them through the
subject, which arandu-permission fills. **A role opens nothing**: a subject that
holds no action is refused everything, an administrator included.

Declare the actions in the permission catalogue beside the application's own.
The customer's and the team's are two lists, so a catalogue an organization
grants from never holds a team action:

```go
permission.Config{
	Actions: append(append(app.Actions(), permission.Actions()...), ticket.Actions()...),
	// ...
}
// and, in the catalogue of the platform's own team only:
//   ticket.TeamActions()
```

| action | side | what it opens |
| --- | --- | --- |
| `ticket.create` | customer | opening a ticket in the subject's own space |
| `ticket.view_own` | customer | reading the tickets the subject asked for |
| `ticket.view_space` | customer | reading every ticket of the space, whoever asked; with `ticket.reply` or `ticket.close`, answering or closing them too |
| `ticket.reply` | customer | adding a message to a ticket the subject can read |
| `ticket.close` | customer | closing a ticket the subject can read, and reopening it |
| `ticket.view_all` | team | reading every space's tickets, internal notes and files included |
| `ticket.manage` | team | answering a customer, and changing a ticket's status and priority |
| `ticket.assign` | team | giving a ticket to a member of the team |
| `ticket.note` | team | writing a note only the team reads |
| `ticket.open_from_contact` | none | the contact form's act; no person holds it |

Each action opens what it names and nothing else, and the Grants for reading
(`ticket.view_own`, `ticket.view_space`, `ticket.view_all`) only read, so the
database refuses a write made with one.

A member of the team is a subject of `Config.TeamTenant` who holds the team's
actions. `TeamPolicy` refuses a subject of any other tenant whatever it carries,
and a subject somebody else is viewing as.

## The three things the application provides

### AccountResolver

Resolves an e-mail to the user who owns it and the space they belong to. Only
`OpenFromContact` asks it.

```go
type accountResolver struct{ users *repositories.Users }

func (a accountResolver) Resolve(ctx context.Context, email string) (ticket.Account, error) {
	user, err := a.users.ByEmail(ctx, email)
	if errors.Is(err, model.ErrModelNotFound) {
		return ticket.Account{}, ticket.ErrNoAccount // no account: nothing is opened
	}
	if err != nil {
		return ticket.Account{}, err
	}
	return ticket.Account{UserID: user.ID, Tenant: user.WorkspaceID, Name: user.Name, Email: user.Email}, nil
}
```

The tenant is the application's answer and nothing a request names. It has to
be a valid tenant, or the call fails and nothing is opened.

### Notifier

Told after the change is stored, when a ticket is opened (`EventOpened`), when
the customer replies (`EventCustomerReplied`) and when the team answers
(`EventTeamReplied`). `Event.Kind.ForTeam()` says who to tell: the team for the
first two, the requester of the ticket for the third. An internal note is never
an event.

```go
type ticketMail struct{ mailer *mail.Mailer }

func (n ticketMail) Notify(ctx context.Context, e ticket.Event) error {
	to := e.Ticket.RequesterEmail // the requester; the team's address for ForTeam()
	if e.Kind.ForTeam() {
		to = "suporte@example.com"
	}
	return n.mailer.Send(ctx, to, e.Ticket.Code()+" "+e.Ticket.Subject, e.Message.Body)
}
```

Its failure does not undo the change: the ticket is open and the reply written,
and a mail server that is down is no reason for a customer to write it twice.
The error is logged (`Config.Logger`) with the event, the ticket and the error,
never the text. An application that must not lose a notification enqueues it
here and delivers it from its queue.

### Directory (optional)

```go
type Directory interface {
	Person(ctx context.Context, tenant, id string) (Person, error) // ErrNoAccount if none
	Team(ctx context.Context) ([]Person, error)                      // who a ticket can be assigned to
}
```

Without one, a ticket keeps the requester's name and e-mail it was given (the
contact form's account carries them), a message shows its author's role in place
of a name, and a ticket can be assigned only to the member of the team who asks.

## The contact form

The application's public contact form opens a ticket only for somebody who has
an account, and does what it did before for anybody else:

```go
result, err := ticketModule.Tickets().OpenFromContact(ctx, ticket.ContactInput{
	Email: in.Email, Name: in.Name, Subject: in.Subject, Body: in.Message,
})
switch {
case errors.Is(err, ticket.ErrNoAccount):
	// no account: nothing was created; only the e-mail goes out, as before
case err != nil:
	return err
}
// result.Ticket is the ticket; result.Duplicate says the same words had
// already opened it. Send the e-mail as you did.
```

The same e-mail, subject and body inside `Config.ContactWindow` (ten minutes by
default) open one ticket, a trailing newline or another case in the address
notwithstanding, and two sent at the same instant are told apart by the tenant's
counter row. There is no session behind the form, so the sender is who the form
says: anybody can type an address. The ticket carries its origin
(`contact_form`), the customer can close a ticket they did not write, and an
application that must not take a form from just anybody puts its own check (a
captcha, a rate limit) in front of the call.

## Publish the views

This package carries the markup of its own five pages and hands it over instead
of rendering it from the inside, because a page you cannot edit is a page that
says the wrong thing in your product.

Look at what would be written, then write it:

```bash
aru vendor:publish --tag=view
aru vendor:publish --tag=view --apply
```

Nothing is written without `--apply`. The preview lists every file as `create`,
`update`, `unchanged` or `conflict`, and running the command a second time
writes nothing. A file changed outside its `arandu:begin custom` markers is
reported as a conflict and left alone; `--force` publishes over one, and even
then what is inside the markers is carried forward.

The files land under `resources/views/modules/ticket/`, and from that point
they are yours: the customer's `index`, `form` and `show`, and the team's
`admin/index` and `admin/show`. Nothing of this package is compiled beside them,
so no view name is registered twice and no rule has to decide which of two files
won, and a view that changes later does not reach a project that already
published it.

Two steps are left to you, and they are left to you because a command that
edited `bootstrap/app.go` behind your back is a command whose output nobody can
explain. Compile what was written:

```bash
aru view:build
```

and import the two directories it wrote into, with the other imports:

```go
	_ "your/module/path/storage/framework/views/modules/ticket"
	_ "your/module/path/storage/framework/views/modules/ticket/admin"
```

Without those imports the views are not in the binary, and the module refuses to
boot rather than answering the first request that reaches one of them with a
500. The refusal names the views, the command and the imports.

The pages extend your `layouts.app` and use the Kyse components
(`components.DataTable` paged, searched, ordered and filtered on the server;
`Field`, `Textarea`, `Select`, `Badge`, `Empty`). They draw no inline style and
no inline script, so a content security policy of `'self'` holds, and each
screen was drawn at 390 px without horizontal scroll: a table scrolls inside its
own container.

## Configuration

| field | required | meaning |
| --- | --- | --- |
| `TeamTenant` | yes | the tenant the platform team signs in to. |
| `Prefix` | no | where the customer's routes are mounted. Defaults to `/tickets`. |
| `TeamPrefix` | no | where the team's routes are mounted. Defaults to `/admin/tickets`. |
| `Paths` | no | the segments of every route. Empty fields keep `DefaultPaths`; `PortuguesePaths` is the set in pt-BR. |
| `PageSize` | no | rows per page. Defaults to 25, refused above 100. |
| `Middleware`, `TeamMiddleware` | no | run after `RequireAuth`, on every route and on the team's only. A guard says "this request is on the right side" and stops; the policy still decides. |
| `Accounts` | no | the `AccountResolver`. Without it `OpenFromContact` refuses. |
| `Directory` | no | names people and lists the team. |
| `Notifier` | no | told about a ticket opened and a reply. Nil tells nobody. |
| `Activity` | no | arandu-activitylog's logger. Nil records nothing. |
| `Markdown` | no | the application's arandu-markdown module. Configure it with `Images: markdown.ImagesNone` and `LinkRel: "noopener nofollow ugc"`, as a text people wrote deserves. Nil shows the text escaped. |
| `Files` | no | the private `filesystem.Disk` for attachments. Nil turns attachments off. |
| `MaxAttachmentBytes` | no | the largest file. Defaults to 5 MiB, refused above 25 MiB. |
| `MaxAttachments` | no | files per message. Defaults to 5, refused above 10. |
| `ContactWindow` | no | how long the same form opens one ticket. Defaults to ten minutes, refused above a day. |
| `Locale` | no | the language of screens, messages and log descriptions. Defaults to `pt-BR`; `en` is shipped too. |
| `Translator` | no | the application's own catalogue, asked before the shipped one, key by key (`ticket.screen.index_title`). |
| `TitleSeparator` | no | joins a screen's title to `APP_NAME` in the document title. |
| `Location` | no | the timezone instants are written in. Defaults to `America/Sao_Paulo`. |
| `Chrome` | no | what the application's layout draws around a screen, carried on the page as `Screen.Chrome()`. |
| `Now`, `Logger` | no | the clock, and where a notifier's failure is logged. |

`New` returns an error rather than starting half-wired: a prefix in the
framework's namespace, two routes that collide, a limit out of range or a team
tenant that cannot be one fail where they are written.

## Routes

Every route sits behind `RequireAuth`: a visitor with no session is sent to
`/auth/login`, and a handler reads who is asking with `ctx.User()`. The paths
below are the defaults; `Config.Prefix`, `Config.TeamPrefix` and `Config.Paths`
move them, and the route names never change. `Module.URLs()` builds every
address, for an application that links to the screens from its own pages.

| method | path | name | |
| --- | --- | --- | --- |
| `GET` | `/tickets` | `ticket.index` | the customer's list |
| `GET` | `/tickets/new` | `ticket.create` | the form |
| `POST` | `/tickets` | `ticket.store` | open one |
| `GET` | `/tickets/{id}` | `ticket.show` | the conversation |
| `POST` | `/tickets/{id}/replies` | `ticket.reply` | reply |
| `POST` | `/tickets/{id}/closure` | `ticket.close` | close |
| `POST` | `/tickets/{id}/reopening` | `ticket.reopen` | reopen |
| `GET` | `/tickets/{id}/attachments/{attachment}` | `ticket.attachment` | a file |
| `GET` | `/admin/tickets` | `ticket.team.index` | the team's list |
| `GET` | `/admin/tickets/{id}` | `ticket.team.show` | the conversation, with its notes |
| `POST` | `/admin/tickets/{id}/replies` | `ticket.team.reply` | answer the customer |
| `POST` | `/admin/tickets/{id}/notes` | `ticket.team.note` | an internal note |
| `POST` | `/admin/tickets/{id}/status` | `ticket.team.status` | change the status |
| `POST` | `/admin/tickets/{id}/priority` | `ticket.team.priority` | change the priority |
| `POST` | `/admin/tickets/{id}/assignee` | `ticket.team.assign` | assign, or take back |
| `GET` | `/admin/tickets/{id}/attachments/{attachment}` | `ticket.team.attachment` | a file |

Every `GET` answers the screen to a browser and JSON to a client that asks for
it (`Accept: application/json`); every form posts to the same routes the JSON
clients use, and a write answers `201`/`200` with the resource to a client that
asks for JSON, and redirects to the conversation otherwise. The list takes
`q`, `status` (repeatable), `sort`, `dir` and `page`; the team's also `priority`
(repeatable), `assignee` (a member's id, `me`, or `none`) and `space`. A sort
the list does not offer, or a status that is not one, is dropped.

| status | what happened |
| --- | --- |
| `303` to `/auth/login` | there is no session on the request |
| `403` | the policy refused: the subject was not granted the action, or is not on the team |
| `404` | no ticket with that id for this subject: it does not exist, it is in another space, or it is somebody else's |
| `303` back to the page the request came from | the input was rejected; the messages (in Portuguese by default) and what was typed are in the flash |
| `422` `application/problem+json` | the input was rejected and the request asked for JSON; the messages are in `errors`, keyed by field |

The package does not choose these: its handlers return what the service
returned, and the framework's router answers it. A refusal carries no detail
beyond the status and its standard sentence.

## Statuses

| status | meaning | moves to |
| --- | --- | --- |
| `open` | nobody on the team answered yet | `waiting_customer` when the team answers |
| `waiting_customer` | the team answered | `waiting_team` when the customer replies |
| `waiting_team` | the customer answered | `waiting_customer` when the team answers |
| `resolved` | the team considers it answered | `waiting_team` when the customer replies or reopens |
| `closed` | nobody replies until it is reopened | `waiting_team` when the customer reopens |

The customer replying to an `open` ticket leaves it `open`. The team can set any
status by hand; the closing instant follows the status. An internal note moves
nothing, tells nobody, and can be written on a closed ticket.

## Attachments

A message may carry up to `MaxAttachments` files, each up to
`MaxAttachmentBytes`: PNG, JPEG, GIF, WebP, PDF or plain text, by what the
bytes are and not by what the sender called them or announced (a script sent as
`.png` is refused). They are kept on `Config.Files` under a key the server
generates (`ticket/attachments/<id>/<sha256>.<ext>`, below the tenant's own
prefix), never under a name the sender typed. The bucket is private: a file is
read only through the download route, which asks the policy, answers a file of
an internal note as not found to the customer, serves it as an attachment with
the detected type, `nosniff` and a `sandbox` policy, and records the reading in
the activity log.

In Arandu applications `Config.Files` is the `filesystem.Disk` of
`FILESYSTEM_DISK=r2` (Cloudflare R2 over the S3 protocol, one bucket per project
and environment); the local disk is for tests.

## What is recorded

With `Config.Activity` the log `ticket` holds one entry per act that changes
something, about the ticket, with the number and the old and new value where
there is one:

`ticket.opened`, `ticket.replied`, `ticket.status_changed`,
`ticket.priority_changed`, `ticket.assigned`, `ticket.closed`,
`ticket.reopened`, `ticket.note_added`, and `ticket.attachment_read`.

An act of the team is written in the customer's tenant, with the team member as
its causer, so it is in the log of the space it happened in and in the platform
administrator's read of every tenant's. No entry carries the text of a message;
the entry of a note says that one was written.

## The data path

`Ticket`, `Message` and `Attachment` embed `model.Model`, and each table is
declared once, beside the entity, in `model.go`. `aru model:build` generates
`TicketQuery.go` and the others from them: `Tickets(db)`, which starts a
`*TicketQuery`, and `TicketCollection`. The files are committed and never edited
by hand; regenerate them after changing an entity, and `aru model:build --check`
fails when one is out of date.

`TicketService` and `TeamService` own `*data.DB` and follow
`validate -> security.Authorize -> Grant -> Model terminal`; handlers never hold
the database or start a query. The tenant of every write is `data.Tenant(g)`,
from the Grant, never from the path, the body, the query string or a header.

Three Grants are issued without a signed-in customer, all in `grants.go` and
nowhere else, each after something else decided:

- the contact form's, after the application's `AccountResolver` named the
  account;
- the team's, scoped to the one tenant a ticket is in, after `TeamPolicy` allowed
  the member and only for the action the realm's Grant was issued for;
- the one that records a file being read.

The team's list reads the tickets table without the tenant filter, through
`internal/platform`, which no application can import. Its rows never leave the
service as anything but snapshots with no connection, and every write goes
through the tenant's own model.

## Layout

```
module.go        registration, routes, the contract with the framework
config.go        what the application passes in, and the route table
contracts.go     what the application implements: accounts, directory, notifier
model.go         the entities, their own rules, and what they may answer with
policy.go        who may do what, for a customer and for the team
service.go       the customer's use cases, and Model access after authorization
team.go          the team's use cases
contact.go       opening a ticket from the contact form
compose.go       storing a ticket, a message and its files
attachments.go   checking and keeping files
grants.go        the three Grants issued without a customer
activity.go      the activity log and the notifier
handlers.go      the controllers of both sides
pages.go         what the screens are handed
screens.go       building it from what a service answered
migrations.go    the schema
translation.go   the sentences, in resources/lang
views.go         the files the application takes ownership of
```

## What is not here

- E-mail: the package sends none and reads none. `OriginEmail` is a value an
  application can open a ticket with; receiving mail is the application's.
- SLAs, ticket merging, categories as a managed list, canned replies, and
  attachments on the contact form.
- A second way to decide who may do what, a local role table or a balance:
  permissions are arandu-permission's, activity is arandu-activitylog's.

## Tests

```bash
export GOWORK=off
go test -race ./...
```

The denial suite constructs the services with a nil database, so even building
`Tickets(nil)` would panic. The structural twin reads every exported service
method and checks the same order on the allowed path, that a system Grant is
issued in `grants.go` alone, that the table that reads every tenant is reached
only after the team's policy answered, and that one place writes unescaped HTML.
The feature suite runs the whole thing over a real SQLite database, and over the
router with sessions. The views are published into a scratch project, compiled
with the view compiler an application runs, and drawn once with sample data.

## Licence

MIT. See [LICENSE.md](LICENSE.md). Copyright HYZIS.
