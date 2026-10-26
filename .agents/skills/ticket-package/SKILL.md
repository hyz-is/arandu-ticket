---
name: ticket-package
description: Install, wire and use the Arandu Ticket package (Go, Arandu) in an application. Use when the request is to "install Arandu Ticket", "add tickets to the app", "go get github.com/hyz-is/arandu-ticket", "wire it into bootstrap/app.go", "register the module", "open a ticket from the contact form", "write the AccountResolver", "write the Notifier", "everything under /tickets returns 403", "it redirects to /auth/login", "the table does not exist", "no such table", "the customer sees an internal note", "move the tickets to /painel/chamados", or when a project's go.mod already requires github.com/hyz-is/arandu-ticket. Covers the wiring and where each line goes, the Config fields, the two sides and their prefixes, the actions to declare in arandu-permission, the AccountResolver, the Notifier and the Directory the application writes, the contact form, the migration step that is not optional, and the views to publish.
license: MIT
metadata:
  audience: app
---

# Using Arandu Ticket

Internal support tickets for a multi-tenant application: a conversation between
a user of a space and the platform team. Two separate sides, each with its own
routes and screens. It is registered by hand: there is no service provider, no
container and no discovery, so if a line is not written in the application, it
does not happen.

## Install

```bash
go get github.com/hyz-is/arandu-ticket
```

## The wiring, and where each line goes

All of it is in `bootstrap/app.go`. Nothing else in the application changes,
and `aru make:module` does not edit this file for you.

The import:

```go
import (
	ticket "github.com/hyz-is/arandu-ticket"
)
```

The construction, in `Build`, after the session store, the activity log and the
markdown module exist and before `k.Register`:

```go
	ticketModule, err := ticket.New(ticket.Config{
		TeamTenant: cfg.Auth.Tenant, // the tenant the platform team signs in to
		Accounts:   accountResolver{users: users},
		Notifier:   ticketMail{mailer: mailer},
		Activity:   activityModule.Logger(),
		Markdown:   markdownModule,
		Files:      filesDisk,
	}, db, sessions)
	if err != nil {
		return App{}, err
	}
```

The registration, inside the `k.Register(...)` call already there:

```go
		ticketModule,
```

`New` refuses a configuration that cannot work, a nil database handle and a nil
session store, so a wiring mistake costs one restart. `TeamTenant` is the one
required setting.

An application that keeps the customer's screens on its panel and the team's on
its operator console mounts the two sides itself, each with its guards:

```go
ticketModule.CustomerRoutes(panel)
ticketModule.TeamRoutes(console)
```

## Then run the migrations and publish the views

```bash
aru migrate
aru vendor:publish --tag=view --apply
aru view:build
```

and import the two directories `aru view:build` wrote
(`storage/framework/views/modules/ticket` and `.../ticket/admin`). Without them
the module refuses to boot and names what is missing.

`aru migrate` is not optional and never runs at boot: with N replicas, N
migrations race each other. **Symptom to recognise:** `no such table`, or
`relation does not exist`, from a route that authorized correctly.

## Declare the actions

Permissions are arandu-permission's, and the policies decide by the actions it
fills on the subject. A role opens nothing. Add both lists to the catalogue,
the team's to the platform team's groups only:

```go
ticket.Actions()     // customer: create, view_own, view_space, reply, close
ticket.TeamActions() // team: view_all, manage, assign, note
```

Mount arandu-permission's `Roles()` in `Config.Middleware`, so the actions are
resolved on each request. Put the operator console's own guard in
`Config.TeamMiddleware`.

**Symptom to recognise:** every route answers `403` to someone who should be
let in. The subject holds no action: the group was not given the action, or
`Roles()` is not mounted. A team action carried by a subject outside
`TeamTenant` is refused on purpose.

## The three things the application writes

- `AccountResolver`: `Resolve(ctx, email) (ticket.Account, error)`. Return
  `ticket.ErrNoAccount` when no account has the e-mail. The tenant in the answer
  is the application's own and has to be a valid tenant.
- `Notifier`: `Notify(ctx, ticket.Event) error`, told when a ticket is opened,
  when the customer replies and when the team answers. `Event.Kind.ForTeam()`
  says who to tell. The package sends no e-mail; a failure of the notifier is
  logged and never undoes the change.
- `Directory` (optional): `Person` and `Team`. Without it a ticket can be
  assigned only to the member who asks.

## The contact form

```go
result, err := ticketModule.Tickets().OpenFromContact(ctx, ticket.ContactInput{
	Email: in.Email, Name: in.Name, Subject: in.Subject, Body: in.Message,
})
if errors.Is(err, ticket.ErrNoAccount) {
	// nothing was created; only the e-mail goes out, as before
} else if err != nil {
	return err
}
```

The same words twice inside `Config.ContactWindow` open one ticket
(`result.Duplicate`). The sender is who the form says: put a captcha or a rate
limit in front of the call.

## Routes

Every route sits behind `RequireAuth`, mounted by the package itself. A request
with no session is sent to `/auth/login`: 303, or `HX-Redirect` for an htmx
request. **Symptom to recognise:** every route redirects to `/auth/login`.
Nobody is signed in on that request: the session cookie did not arrive, or the
session store passed to `New` is not the one the application signs people in
with.

Defaults: the customer's under `/tickets` (`ticket.index`, `ticket.create`,
`ticket.store`, `ticket.show`, `ticket.reply`, `ticket.close`, `ticket.reopen`,
`ticket.attachment`), the team's under `/admin/tickets` (`ticket.team.index`,
`.show`, `.reply`, `.note`, `.status`, `.priority`, `.assign`, `.attachment`).
`Config.Prefix`, `Config.TeamPrefix` and `Config.Paths` (`PortuguesePaths`) move
them; the names never change, so build addresses from `Module.URLs()`.

| status | what happened |
| --- | --- |
| `303` to `/auth/login` | there is no session on the request |
| `403` | the subject was not granted the action, or is not on the team |
| `404` | no such ticket for this subject: it does not exist, is in another space, or is somebody else's |
| `303` back | the input was rejected; the messages and what was typed are in the flash |
| `422` `application/problem+json` | the same, for a client that asked for JSON |

## Attachments

`Config.Files` is the application's private Disk (Cloudflare R2 in production).
A message carries images, PDF or plain text, checked by content. Without a Disk,
a message that carries a file is refused.

## Reporting a problem

Issues and pull requests go to the repository the module path names,
`github.com/hyz-is/arandu-ticket`, which belongs to `hyz-is`. A vulnerability
goes to the private advisory form named in that repository's `SECURITY.md`, and
never into an issue.

MIT licensed. Copyright HYZIS.
