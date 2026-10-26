---
name: ticket-policy
description: Decide who may do what in this Arandu package, and change an action. Use when the request is to "add an action", "add a permission", "let the owner close tickets", "everything returns 403", "the tests say ErrForbidden", "add a Model-backed service method", "why does Find authorize twice", "filter the list by requester", "let the team do X", "a system grant", or when a change touches policy.go, service.go, team.go, contact.go or grants.go. Covers the two policies, the Grant, authorization before the Model, the custom block that survives regeneration, why the tenant is never read from the request, the three Grants issued without a customer, and the shapes that look like authorization and are not.
license: MIT
---

# The policies

There are two, and they answer for different people.

- `TicketPolicy` is the customer's. It allows an action to a subject that was
  granted it (`s.Can(action)`, filled by arandu-permission), on a ticket of the
  subject's own tenant, and for the actions about a stored ticket only when the
  subject asked for it or holds `ticket.view_space`.
- `TeamPolicy` is the platform team's. It allows an action to a subject of
  `Config.TeamTenant` that was granted it, and refuses a guest and a subject
  somebody else is viewing as. It is the one policy that does not compare the
  record's tenant with the subject's: crossing tenants is what the team does.

Neither has a branch that allows an action by role. A subject that holds no
action, an administrator included, is refused everything; the default-deny test
walks every action with one.

## How a request actually gets through

1. The service validates the input. A rejected input never reaches a policy
   (the customer's side; the team's `reach` runs first because it also finds the
   ticket).
2. The service calls `security.Authorize(ctx, s.policy, actor, ACTION, record)`.
3. `Can` returns nil, or the reason it will not.
4. Only then does a `security.Grant` exist, and the service reaches
   `Tickets(s.db)` with it.

The Model requires a Grant and applies its tenant, but it does not decide which
action the Grant represents, so `Authorize` before the first `Tickets(s.db)` is
mandatory even though the terminal also takes `g`.

## The procedure for an action

**1. Declare it** in `policy.go` with a doc comment, and put it in `Actions()`
or `TeamActions()`, never both. Say in `reads` whether it only looks: an action
not listed there issues a Grant that writes.

**2. Write the rule inside the custom block.** Nothing regenerates the file in
this repository, so the markers cost nothing and buy one thing: the same policy
regenerated elsewhere keeps what is between them. What is not written there
stays closed.

**3. Leave the tenant check above the block alone.** It runs before every rule:
a rule that allows an owner to read their own record would otherwise allow it
across customers. The test asserts the message contains `another tenant`.

**4. Add it to the tests.** `everyAction` in `tests/Unit/policy_test.go` is the
whole set the default-deny tests walk; a list holding eight of nine passes while
the ninth is open. Add the rule's cases to the table beside it, and name the
action in the changelog (`TestEveryActionIsNamedInAReleasedChangelogEntry`).

## Reads are authorized like writes

`Find` authorizes twice: the first call sees an empty ticket, so every rule about
the record never ran; the second, on the row, is the one that keeps the policy
honest. A ticket the second call refuses is answered `ErrNotFound`, as one of
another space is: telling a customer that a ticket exists and is not theirs
tells them what exists. `List` authorizes once and narrows the statement by a
predicate (`requester_id`), because a policy call per row would still read the
rows.

## The team crosses tenants in two steps

`TeamService.reach` asks `TeamPolicy` for a realm Grant, finds the ticket's
tenant through `internal/platform` (the tickets table with no tenant filter),
and then acts with a Grant scoped to that one tenant. The scoped Grant is issued
by `scopedGrant` in `grants.go`, which checks the realm Grant was issued for the
same action. Nothing a request names chooses the tenant.

## The Grants issued without a customer

`grants.go` is the only file that builds a Grant without a Policy answering for
it, and `TestAGrantWithoutACustomerIsIssuedInGrantsGoAlone` holds that to three,
each marked `//arandu:system-grant` with the reason:

- `contactGrant`, after the application's `AccountResolver` named the account;
- `scopedGrant`, after `TeamPolicy` allowed the member;
- `recordGrant`, for the entry that says a file was read.

A fourth is a decision to take in review, not a line to find in a diff. Do not
reach for `security.SystemGrant` anywhere else in this package.

## Shapes that look like authorization and are not

- **A tenant taken from the request.** The path, the body, the query string, a
  header: values the caller chose. `data.Tenant(g)` is the only source, and
  `TestNoTenantIsReadOutOfTheRequest` fails on the others. The team's `space`
  filter is a predicate for someone who already reads every tenant, never a
  scope.
- **A check in the handler.** Handlers read the input and who is asking, ask the
  service, and return what it returned. `TestHandlersLeaveTheSubjectAndTheStatusToTheFramework`
  fails on one that holds the database or constructs a query.
- **A refusal that explains itself to the client.** The router answers with the
  status and its standard sentence; the reason is in the log.
- **A button hidden by `auth.Allows`.** It decides what to draw, never whether to
  do it; the route behind the button asks the policy again.

## Run the gates

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go') \
  && go build ./... && go vet ./... && go test -race -p 2 ./...
```
