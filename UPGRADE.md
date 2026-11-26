# Upgrade Guide

## v0.3.0

Backward compatible: an application that changes nothing behaves as before.
Visitors are off until `Config.VisitorTenant` is set, and the new `Origin`
value only appears when an application asks for it.

### Turn visitors on (optional)

Pick a tenant for the visitors' queue that the application never gives to an
account and that is not `TeamTenant`, read it from configuration, and pass it:

```go
ticket.Config{TeamTenant: "platform", VisitorTenant: "visitors"}
```

Then call `OpenFromVisitor` from the public form for an e-mail that has no
account (`OpenFromContact` answered `ErrNoAccount`), or from a WhatsApp
conversation with somebody the application does not know, with a `Thread`. The
team reads the queue in its own list, with the `space` filter or
`URLs.TeamVisitors`. Put a captcha or a rate limit in front of a public form:
there is no session behind the call.

Call `AdoptVisitorTickets` only when the person proved the e-mail (the link of
the confirmation e-mail was opened), never with an address somebody typed.

### Nothing to migrate

No migration is added: the visitors' tickets live in the existing tables under
the queue's tenant.

### The team's views

The team's list and conversation draw a visitor badge and a link to the
queue. A project that published the views republishes them:

```sh
aru vendor:publish --tag=view
aru vendor:publish --tag=view --apply
aru view:build
```

A project that does not republish keeps working and simply does not show the
badge.

### Where visitors' entries land in the log

The log of a visitor's act has the system as its causer, and the entry of an
adoption is written in the account's space with the account as its causer.
An application that reads `ticket` entries by causer sees the new value
`system`.

## v0.2.1

Raises the minimum versions of Hesape, Framework and Kyse. Nothing else.

## v0.2.0


### The lists are drawn as Kyse's data table now draws them

This release builds against Kyse v0.53.0, whose `DataTable` takes one `State`
where it took a query, a sort and a list of chosen values. Nothing a caller
writes changes: `ListQuery`, `TeamQuery`, `Page` and the services keep their
shape, and so do the routes and the JSON the lists answer. What changes is the
page data, the views and the catalogue.

- `IndexPageData` and `TeamIndexPageData` gain `Hidden`, the keys of the columns
  the reader switched off, read from the `hide` parameter, and each gains a
  `TableState()` method, which is what the views hand to the table as its
  `State`. `TeamIndexPageData` also gains `PerPage`, and its `Space` field,
  which the handler never filled, now carries the `space` the list was asked
  for, so a step of the table keeps it. The old fields stay and are filled as
  before.
- The two list views (`index` and `admin/index`) draw `Searchable`, `State`,
  `FullPager` and `HeaderMenu`, and no longer set `Query`, `SearchName`,
  `SortKey`, `SortDir` or `DataTableFilter.Selected`, which Kyse removed. A
  project that published them republishes, or removes those fields by hand:

```sh
aru vendor:publish --tag=view
aru vendor:publish --tag=view --apply
aru view:build
```

- `Screen.TableLines` fills the sentences of Kyse's new table, from the `table`
  block of the catalogue. `table.showing`, `table.per_page` and `table.page` are
  no longer asked for; `table.filter`, `rows`, `rows_one`, `rows_many`,
  `selected_count`, `rows_per_page`, `first`, `last`, `select_row`, `reset`,
  `clear_filters`, `no_facet_results`, `facet_selected`, `open_menu`, `actions`,
  `toggle_columns`, `asc`, `desc` and `hide` are, and ship in `en` and `pt-BR`.
  An application that overrides the table's words in its own catalogue adds the
  new keys under `ticket.table.`; a line it leaves out says the English.

The lists are still searched, ordered and paged by the services with an
allowlist of sort keys, and not by a `pagination.Listing`: the search matches a
ticket number as well as text, the team's list orders by the rank of a priority
and filters by an assignee that is not a fixed set of values, and a listing
declares columns and values and nothing else.

## v0.1.0

First release: nothing to upgrade from. A new installation reads the README, in
this order: `go get`, the wiring (`TeamTenant` is the one required setting), the
actions in the arandu-permission catalogue, `aru migrate`, and
`aru vendor:publish` with `aru view:build` for the screens. The package requires
Framework v0.66.0, Hesape v0.85.0, arandu-activitylog v0.7.0 and
arandu-markdown v0.4.0, and Go 1.26.10.

