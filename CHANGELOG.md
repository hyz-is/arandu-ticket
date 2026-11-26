# Changelog

Everything worth knowing about a release of Arandu Ticket is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

## [0.4.0] - 2026-11-26

### Added

- The team's list can be narrowed to the channel a ticket came in by:
  `TeamQuery.Origins`, the `origin` parameter of the list, a filter and a
  switchable column of the data table, `TeamIndexPageData.Origins` and
  `OriginOptions`, and `Screen.OriginOptions`. `Origins` lists the four channels
  (`web`, `contact_form`, `email`, `whatsapp`), and `origin` is a sort key too.
  The origin was already stored on every ticket; an application that wants its
  own words for the channels ("Painel", "Site") overrides `origin.web` and
  `origin.contact_form` in its catalogue, and the values stay. An origin that is
  none of the four is refused with the `origin` message.
- `Config.CustomerMiddleware`, the customer's side alone as `TeamMiddleware` is
  the team's. The order on a customer route is `Middleware`, then
  `CustomerMiddleware`; on a team route, `Middleware`, then `TeamMiddleware`.
  `Middleware` keeps running on both, as before.
- `teamdir`, a package of this module with a `ticket.Directory` that makes the
  platform team the members of an arandu-permission group: `teamdir.New` takes
  the permission service, the realm, the group's slug and a `Names` lookup, and
  every member of the group can be given a ticket and shows by name. The module's
  root imports nothing new; the module requires arandu-permission v0.13.3 for
  this package alone.
- The list's `space` parameter (the visitors' queue filter) now travels with the pager and
  every address of the table (it was carried by the table's state only).


## [0.3.0] - 2026-11-26

### Added

- Tickets for people with no account. `Config.VisitorTenant` names the
  visitors' queue, a tenant of its own that no customer's subject belongs to,
  and `TicketService.OpenFromVisitor` opens a ticket in it from the application's
  contact form, a received e-mail or a WhatsApp conversation
  (`VisitorInput`, `VisitorResult`). The team reads and answers it as any other
  space's ticket, and its answer reaches the Notifier as `EventTeamReplied` with
  the visitor's e-mail as the requester's. Without a tenant the call answers
  `ErrVisitorsOff` and nothing is read.
- Conversations: a `VisitorInput.Thread` (a WhatsApp number, a mail thread) makes
  the next message of the same conversation join the ticket that is not closed
  (`VisitorResult.Appended`) instead of opening another, and the same words
  twice inside `Config.ContactWindow` are a repeat (`Duplicate`). The thread is
  kept only as a digest.
- `TicketService.AdoptVisitorTickets` moves the visitors' tickets whose
  requester e-mail is a proven account's into that account's space, with a new
  number, their messages and the team's triage, and a line the customer reads
  saying the contact was linked to the account. A ticket that carries a file
  stays in the queue and is counted in the log. Calling it twice moves nothing
  the second time.
- Two actions no person holds, `TicketOpenFromVisitor` and `TicketAdopt`, and
  the two `Grant`s that exercise them, built in `grants.go` beside the
  existing ones. Two events, `ticket.visitor_replied` and `ticket.adopted`;
  a visitor's entries have the system as their causer, and the adoption's has
  the account.
- `OriginWhatsApp`, and `OpenRequest.Origin`, so an application that opens a
  ticket for a signed-in customer from its own channel (an assistant on
  WhatsApp acting as the person) says which channel it was. The screens still
  open `OriginWeb`, and `OriginContactForm` is refused there.
- The team's list marks a visitor's ticket with a badge, `Summary.Visitor` and
  `visitor` in the team's JSON say so, and `URLs.TeamVisitors` and
  `TeamIndexPageData.VisitorsURL` link to the list narrowed to the queue. The
  published team views (`admin/index`, `admin/show`) draw the badge and the
  link: republish them with `aru vendor:publish --tag=view --apply`.
- Catalogue lines in `en` and `pt-BR`: `screen.visitor_badge`,
  `screen.visitor_space`, `screen.visitors_link`, `field.thread`,
  `origin.whatsapp`, `validation.origin`, `message.adopted` and the two activity
  descriptions.

### Changed

- `OpenFromContact` and `OpenFromVisitor` validate the subject and the body
  with the same rules, and the answers of the team's status, priority and
  assignee changes carry `visitor` too. Nothing a caller wrote before changes.

## [0.2.1] - 2026-11-15

### Changed

- Requires Hesape v0.109.0, Framework v0.71.0 and Kyse v0.53.1.

## [0.2.0] - 2026-11-15

### Changed
- The customer's and the team's lists are drawn with Kyse's server-side data
  table: the views hand it a `State` (`IndexPageData.TableState` and
  `TeamIndexPageData.TableState`) instead of a query, a sort and the chosen
  values, and the columns a reader switched off survive a search, an order and a
  page through the `hide` parameter. The `space` a team list was asked for is
  carried by every address of the table. Requires Kyse v0.53.0.
- The `table` block of the catalogue names the sentences of the new table, in
  `en` and `pt-BR`; `table.showing`, `table.per_page` and `table.page` are gone.

## [0.1.0] - 2026-10-26

### Added

- Internal support tickets, for the customers of a multi-tenant application and
  for the platform team that answers them, as two separate sides: each has its
  own controllers, screens and prefix, and the application mounts each where its
  own session is valid (`Module.CustomerRoutes`, `Module.TeamRoutes`, or
  `Module.Routes` for both).
- `Ticket`: a number that is sequential and readable inside its tenant
  (`#000123`, taken from a locked counter row and held unique by an index), a
  subject, a status (`open`, `waiting_customer`, `waiting_team`, `resolved`,
  `closed`), a priority (`low`, `normal`, `high`, `urgent`), an optional short
  category, the requester, an optional assignee from the team, the origin
  (`web`, `contact_form`, `email`) and its creation, update and closing
  instants. Its own rules are pure methods: `ReceiveCustomerReply`,
  `ReceiveTeamReply`, `Close`, `Reopen`, `SetStatus`, `SetPriority`, `Assign`,
  `AddNote`.
- `Message` (author, role `customer`, `team` or `system`, plain text with
  optional Markdown, and the `internal` flag of a note only the team reads) and
  `Attachment` (a file kept on the application's `Config.Files` Disk under a key
  the server generates, whose type is detected and not announced; images, PDF
  and plain text only, with a size and a count limit).
- The customer's side: `TicketService` with `List` (a data table paged,
  searched, ordered and filtered by status on the server), `Open`, `Find`,
  `Reply`, `Close`, `Reopen` and `Attachment`. A customer reads the tickets
  they asked for, or the whole space's with `ticket.view_space`; a ticket of
  another space, and one of the same space the subject may not read, are not
  found.
- The team's side: `TeamService` with `List` (every tenant's tickets, filtered by
  status, priority, assignee and space, searched by number, subject, requester
  name and e-mail), `Find`, `Reply`, `Note`, `SetStatus`, `SetPriority`,
  `Assign`, `Team` and `Attachment`. The team reads the internal notes; the
  customer is never shown one, nor the team's priority and assignee.
- `TicketService.OpenFromContact`: opens a ticket from the application's public
  contact form, only for an e-mail that belongs to an account (the
  application's `AccountResolver`), and answers `ErrNoAccount` for any other.
  The same e-mail, subject and body sent twice inside `Config.ContactWindow`
  open one ticket, including two sent at once. `ContactInput`, `ContactResult`.
- The three things the application provides: `AccountResolver` (e-mail to user
  and tenant), `Notifier` (told when a ticket is opened, when the customer
  replies and when the team answers; it sends the e-mail, this package sends
  none, and its failure never undoes the change), and the optional `Directory`
  (names people and lists the team). `Account`, `Person`, `Event`, `EventKind`
  with `EventOpened`, `EventCustomerReplied` and `EventTeamReplied`.
- Actions, filled by arandu-permission and decided by the policies in Go:
  `TicketCreate`, `TicketViewOwn`, `TicketViewSpace`, `TicketReply` and
  `TicketClose` for the customer (`Actions()`), `TicketViewAll`,
  `TicketManage`, `TicketAssign` and `TicketNote` for the team (`TeamActions()`,
  declared apart so no organization's catalogue holds them), and
  `TicketOpenFromContact`, which no person holds. `TicketPolicy` and
  `TeamPolicy` deny by default, answer for a subject who was granted the action
  and nobody else, issue read-only Grants for reading, and `TeamPolicy` refuses
  any subject outside `Config.TeamTenant` and any subject viewing as somebody.
- Records in arandu-activitylog (`Config.Activity`, log `ticket`) for
  `ticket.opened`, `ticket.replied`, `ticket.status_changed`,
  `ticket.priority_changed`, `ticket.assigned`, `ticket.closed`,
  `ticket.reopened`, `ticket.note_added` and `ticket.attachment_read`. No entry
  carries the text of a message, and the entry of an internal note says only
  that a note was written.
- Markdown rendered by arandu-markdown (`Config.Markdown`), sanitized; without
  it the text is shown escaped.
- Five screens built on Kyse components (the customer's list, the form, the
  conversation, the team's list and the team's conversation), published with
  `aru vendor:publish`, in Brazilian Portuguese and English
  (`Config.Locale`, default `pt-BR`; `Config.Translator` overrides any line),
  with empty and error states, and usable at 390 px without horizontal scroll.
- The migration `20261026_0001_create_tickets`: `tickets`, `ticket_messages`,
  `ticket_attachments` and `ticket_counters`.
- Routes, all behind `RequireAuth`: `ticket.index`, `ticket.create`,
  `ticket.store`, `ticket.show`, `ticket.reply`, `ticket.close`,
  `ticket.reopen`, `ticket.attachment`, and on the team's side
  `ticket.team.index`, `ticket.team.show`, `ticket.team.reply`,
  `ticket.team.note`, `ticket.team.status`, `ticket.team.priority`,
  `ticket.team.assign` and `ticket.team.attachment`. `Config.Prefix`,
  `Config.TeamPrefix` and `Config.Paths` (`DefaultPaths`, `PortuguesePaths`)
  move them; the names never change.

