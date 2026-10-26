# Changelog

Everything worth knowing about a release of Arandu Ticket is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

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

