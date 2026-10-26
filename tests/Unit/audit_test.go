package unit_test

import (
	"go/ast"
	"go/build/constraint"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What this package proves about itself, before anybody installs it.
//
// `aru doctor` audits the application it is run inside. It walks that project's
// own tree, skips vendor/, reads the one arandu.mod.toml at its root, and
// refuses a directory that is not an application at all. It never loads a
// dependency. So nothing an installed package does is audited by the
// application that installed it: a package that reached a table without a
// Grant, took a tenant out of a request, or declared no capabilities and then
// opened a socket would pass every check the installer runs.
//
// The package is therefore the only place that check can happen, and this is
// it. These tests read this package's own Go files as syntax and hold four
// properties:
//
//  1. every exported Service method calls Authorize before it reaches the
//     generated query;
//  2. the tenant comes from the Grant, and nothing a request carried is read as
//     one;
//  3. the Model remains tenant-scoped and CRUD does not grow a second data path;
//  4. what arandu.mod.toml declares is what the code does.
//
// The fifth is held where the routes exist rather than here, because a prefix
// arrives through configuration and syntax cannot follow it:
// TestNoRouteLandsInTheFrameworkNamespace, in tests/Feature/routes_test.go,
// registers the module and reads the table back.
//
// What these tests do not reach is worth as much as what they do. They read
// syntax: a call hidden behind an interface, a wrapper around the named Model
// entry point, and anything reached by reflection are all invisible to them. A
// green run means no such thing was found written down, not that none exists.
// What is absolute is what the compiler holds alongside them -- a Model terminal
// with no Grant in its call does not compile.

// buildable reports whether the compiler ever reads this file.
//
// It asks the build constraint rather than the file name, so a file excluded by
// a tag is left out of the audit whatever it is called: what the compiler never
// builds is not part of what the package does, and reporting it as such would
// be reporting a capability nobody can reach.
func buildable(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			expression, err := constraint.Parse(comment.Text)
			if err != nil {
				continue
			}
			// No tag set, which is what the gates run with.
			if !expression.Eval(func(string) bool { return false }) {
				return false
			}
		}
	}
	return true
}

// linked reports whether an application that installs this package compiles
// this file into its binary.
//
// A command is not linked. `go get` of a library never pulls in the main
// package beside it, and `go build` of the application never reaches it, so
// what a command does is not a capability anybody who installs this agreed to.
// Auditing one would make the manifest declare a capability that no running
// application has -- which is the same defect as declaring one nothing uses,
// pointed the other way.
//
// The package carries no command today, and the rule is here rather than in the
// commit that adds one: a capability audit that grows a hole the first time
// somebody needs it is an audit whose answer depends on who ran it.
//
// Every rule in this file reads what the installer links, and this is where
// that is decided once.
func linked(file *ast.File) bool { return file.Name.Name != "main" }

// auditedFiles is every Go file an application that installs this package
// compiles into its binary.
func auditedFiles(t *testing.T) []parsedGoFile {
	t.Helper()

	out := []parsedGoFile{}
	for _, source := range productionGoFiles(t, packageRoot(t)) {
		if buildable(source.file) && linked(source.file) {
			out = append(out, source)
		}
	}
	// An audit with nothing to read passes, and a test that passes by finding
	// nothing is the failure mode of every rule below.
	if len(out) == 0 {
		t.Fatal("no buildable Go file was found, so everything below would pass by having nothing to read")
	}
	return out
}

// selectorName is the trailing name of a selector, or the name of an
// identifier.
func selectorName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.SelectorExpr:
		return expression.Sel.Name
	case *ast.Ident:
		return expression.Name
	}
	return ""
}

// calledName is the trailing name of whatever a call names, so a call can be
// recognised without resolving what it is called on.
func calledName(call *ast.CallExpr) string {
	return selectorName(call.Fun)
}

// firstCallTo is where a body first calls something by this name.
func firstCallTo(body *ast.BlockStmt, name string) token.Pos {
	found := token.NoPos
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || calledName(call) != name {
			return true
		}
		if found == token.NoPos || call.Pos() < found {
			found = call.Pos()
		}
		return true
	})
	return found
}

// services are the types whose exported methods are the use cases of a side.
var services = map[string]bool{"TicketService": true, "TeamService": true}

// deciders are what a use case may ask before it reaches the Model, and each is
// either security.Authorize itself or a helper that is checked below to call
// it, or to issue the one Grant that needs no Policy.
var deciders = map[string]bool{
	"Authorize": true, "authorizeView": true, "reach": true, "contactGrant": true,
}

// modelReaches are the calls through which a use case first touches data: the
// generated queries, the write terminals, and the helpers of core that run
// statements.
var modelReaches = map[string]bool{
	"Tickets": true, "Messages": true, "Attachments": true, "Counters": true,
	"Save": true, "Delete": true, "Restore": true, "Touch": true,
	"create": true, "add": true, "conversation": true, "open": true, "putFiles": true,
}

// TestEveryServiceMethodAuthorizesBeforeTheModel holds the mandatory path on
// every exported use case of both sides rather than on the ones that exist
// today.
//
// A nil-database denial test proves the closed path. This syntax audit is its
// twin for an allowed path: Model construction is visible in the method body,
// and moving it above the decision fails even when no terminal is executed.
func TestEveryServiceMethodAuthorizesBeforeTheModel(t *testing.T) {
	t.Parallel()

	audited := map[string]int{}
	for _, source := range auditedFiles(t) {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || !services[receiverType(function)] || !function.Name.IsExported() {
				continue
			}
			audited[receiverType(function)]++

			decided := firstCallToAny(function.Body, deciders)
			reach := firstCallToAny(function.Body, modelReaches)
			if decided == token.NoPos {
				t.Errorf("%s: %s never asks a Policy, so nothing decided whether the Model may run", source.path, function.Name.Name)
			}
			if decided != token.NoPos && reach != token.NoPos && decided > reach {
				t.Errorf("%s: %s reaches the Model before it is authorized", source.path, function.Name.Name)
			}
		}
	}
	for service := range services {
		if audited[service] == 0 {
			t.Fatalf("no exported %s method was found, so this test proved nothing", service)
		}
	}
}

// TestTheHelpersThatDecideActuallyDecide holds the helpers the audit above
// trusts: each calls security.Authorize, except the one that issues the contact
// form's Grant, which is held to grants.go below.
func TestTheHelpersThatDecideActuallyDecide(t *testing.T) {
	t.Parallel()

	found := map[string]bool{}
	for _, source := range auditedFiles(t) {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || !deciders[function.Name.Name] || function.Name.Name == "Authorize" {
				continue
			}
			found[function.Name.Name] = true
			switch function.Name.Name {
			case "contactGrant":
				if source.path != "grants.go" {
					t.Errorf("%s: contactGrant lives outside grants.go", source.path)
				}
			default:
				if firstCallTo(function.Body, "Authorize") == token.NoPos {
					t.Errorf("%s: %s is trusted to decide and never calls security.Authorize", source.path, function.Name.Name)
				}
			}
		}
	}
	for name := range deciders {
		if name != "Authorize" && !found[name] {
			t.Errorf("the decider %s is listed and does not exist", name)
		}
	}
}

// firstCallToAny is where a body first calls anything by one of these names.
func firstCallToAny(body *ast.BlockStmt, names map[string]bool) token.Pos {
	found := token.NoPos
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !names[calledName(call)] {
			return true
		}
		if found == token.NoPos || call.Pos() < found {
			found = call.Pos()
		}
		return true
	})
	return found
}

// requestAccessors are the ways a value that arrived with the request is read.
// A tenant taken through any of them is a tenant the caller chose.
var requestAccessors = map[string]bool{
	"Param":         true,
	"Query":         true,
	"Input":         true,
	"FormValue":     true,
	"PostFormValue": true,
	"PathValue":     true,
	"Cookie":        true,
	"Get":           true,
}

// TestNoTenantIsReadOutOfTheRequest is the rule with no exception in it. A
// tenant the client can name is a client who chooses whose rows they read, and
// every other check in the package passes while it happens.
func TestNoTenantIsReadOutOfTheRequest(t *testing.T) {
	t.Parallel()

	for _, source := range auditedFiles(t) {
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !requestAccessors[selector.Sel.Name] {
				return true
			}
			for _, argument := range call.Args {
				literal, ok := argument.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				if strings.Contains(strings.ToLower(literal.Value), "tenant") {
					t.Errorf("%s: %s(%s) reads a tenant out of the request; it comes from the Grant, which came from the session",
						source.path, selector.Sel.Name, literal.Value)
				}
			}
			return true
		})
	}
}

// TestTheTenantIsWrittenOnlyFromTheGrant holds the write half of tenant
// isolation. The proposed value is policy input; the value persisted after
// Authorize must take TenantID directly from data.Tenant(g), and a tenant_id
// written by key must come from a variable the same function filled from it.
func TestTheTenantIsWrittenOnlyFromTheGrant(t *testing.T) {
	t.Parallel()

	assignments, keyed := 0, 0
	for _, source := range auditedFiles(t) {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.AssignStmt:
					for i, target := range node.Lhs {
						if i >= len(node.Rhs) || selectorName(target) != "TenantID" {
							continue
						}
						// A model's field is written; a value read back from the
						// row (the tenant of a ticket found by the team) is not
						// a write the package does, and is the only exception: it
						// is passed to scopedGrant, which validates it.
						assignments++
						call, ok := node.Rhs[i].(*ast.CallExpr)
						if !ok || calledName(call) != "Tenant" {
							t.Errorf("%s: %s writes TenantID from something other than data.Tenant(g)", source.path, function.Name.Name)
						}
					}
				case *ast.KeyValueExpr:
					literal, ok := node.Key.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING || literal.Value != `"tenant_id"` {
						return true
					}
					keyed++
					if _, ok := node.Value.(*ast.Ident); !ok || firstCallTo(function.Body, "Tenant") == token.NoPos {
						t.Errorf("%s: %s writes tenant_id by key from something other than data.Tenant(g)", source.path, function.Name.Name)
					}
				}
				return true
			})
		}
	}
	if assignments == 0 || keyed == 0 {
		t.Fatalf("found %d tenant assignments and %d keyed ones, so this test proved nothing", assignments, keyed)
	}
}

// TestAGrantWithoutACustomerIsIssuedInGrantsGoAlone holds the three places a
// Grant is built without a Policy answering for it to one file, and each of
// them to a comment that says what decided first. A fourth place is a decision
// to take in review, not a line to find in a diff.
func TestAGrantWithoutACustomerIsIssuedInGrantsGoAlone(t *testing.T) {
	t.Parallel()

	root := packageRoot(t)
	calls := 0
	for _, source := range auditedFiles(t) {
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || (calledName(call) != "SystemGrant" && calledName(call) != "SystemGrantActing") {
				return true
			}
			calls++
			if source.path != "grants.go" {
				t.Errorf("%s: builds a system Grant outside grants.go", source.path)
			}
			return true
		})
	}
	if calls != 3 {
		t.Errorf("grants.go issues %d system Grants, want the three it documents", calls)
	}

	body, err := os.ReadFile(filepath.Join(root, "grants.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "//arandu:system-grant "); got != 3 {
		t.Errorf("grants.go marks %d system Grants with //arandu:system-grant and a reason, want 3", got)
	}
	// A scoped Grant is checked against the realm Grant it comes from, and
	// every one is for a tenant that can be one.
	if !strings.Contains(string(body), "realm.Check(a)") || strings.Count(string(body), "security.ValidTenant(tenant)") < 3 {
		t.Error("grants.go does not check the realm Grant and the tenant before it issues a Grant")
	}
}

// TestOnlyTheTeamsServiceReadsEveryTenant holds the one read that crosses
// tenants to the team's files, and to a Policy asked first.
func TestOnlyTheTeamsServiceReadsEveryTenant(t *testing.T) {
	t.Parallel()

	reads := 0
	for _, source := range auditedFiles(t) {
		for _, imported := range source.file.Imports {
			if !strings.HasSuffix(strings.Trim(imported.Path.Value, `"`), "/internal/platform") {
				continue
			}
			if source.path != "team.go" && source.path != "model.go" {
				t.Errorf("%s imports the table that reads every tenant", source.path)
			}
		}
		if source.path != "team.go" {
			continue
		}
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			reach := token.NoPos
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || calledName(call) != "Tickets" {
					return true
				}
				if owner, ok := call.Fun.(*ast.SelectorExpr); ok {
					if pkg, ok := owner.X.(*ast.Ident); ok && pkg.Name == "platform" && (reach == token.NoPos || call.Pos() < reach) {
						reach = call.Pos()
					}
				}
				return true
			})
			if reach == token.NoPos {
				continue
			}
			reads++
			if decided := firstCallTo(function.Body, "Authorize"); decided == token.NoPos || decided > reach {
				t.Errorf("team.go: %s reads every tenant's tickets before TeamPolicy answered", function.Name.Name)
			}
		}
	}
	if reads != 2 {
		t.Errorf("found %d reads of every tenant's tickets, want the list and the lookup that finds a ticket's tenant", reads)
	}
}

// TestOnlyOneFileWritesUnescapedHTML holds the one place text a person typed
// becomes markup. Everything else on a page is escaped by the view.
func TestOnlyOneFileWritesUnescapedHTML(t *testing.T) {
	t.Parallel()

	found := 0
	for _, source := range auditedFiles(t) {
		ast.Inspect(source.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || calledName(call) != "HTML" {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selectorName(selector.X) == "template" {
				found++
				if source.path != "screens.go" {
					t.Errorf("%s: writes markup with template.HTML outside screens.go", source.path)
				}
			}
			return true
		})
	}
	if found != 1 {
		t.Errorf("found %d conversions to template.HTML, want the one that wraps the escaped text", found)
	}
}

// capabilities is what arandu.mod.toml declares, and what the code does, under
// the same four names.
type capabilities struct {
	network    bool
	filesystem bool
	exec       bool
	migrations bool
}

// TestTheDeclaredCapabilitiesAreWhatTheCodeDoes is the audit the installer
// cannot run. `aru doctor` compares an application's manifest against the
// application's code; the manifest of a package it installed is never opened,
// so this comparison exists here or nowhere.
//
// Both directions fail. Using more than was declared is the one that costs
// somebody something, and the doctor calls it an error too. Declaring more than
// is used is milder -- a warning, there -- and it fails here because `go test`
// has one outcome and because asking for what is not needed is how a
// declaration stops being worth reading.
func TestTheDeclaredCapabilitiesAreWhatTheCodeDoes(t *testing.T) {
	t.Parallel()

	declared := declaredCapabilities(t)
	used := usedCapabilities(auditedFiles(t))

	for _, capability := range []struct {
		name        string
		declared    bool
		used        bool
		consequence string
	}{
		{"network", declared.network, used.network,
			"the code makes calls that leave the process, and whoever installs it agreed to a package that does not"},
		{"filesystem", declared.filesystem, used.filesystem,
			"the code reads or writes files outside the database, which nothing in its API shows"},
		{"exec", declared.exec, used.exec,
			"the code runs another program, which is the widest capability there is"},
		{"migrations", declared.migrations, used.migrations,
			"the code owns tables, and an installer who did not expect that will not know to migrate before deploying"},
	} {
		switch {
		case capability.used && !capability.declared:
			t.Errorf("arandu.mod.toml declares %s = false and the code uses it: %s. Declare it, or remove what needs it",
				capability.name, capability.consequence)
		case capability.declared && !capability.used:
			t.Errorf("arandu.mod.toml declares %s = true and nothing uses it: set %s = false, or the declaration stops being worth reading",
				capability.name, capability.name)
		}
	}
}

// declaredCapabilities reads the [permissions] block.
//
// By hand, because the alternative is a third dependency in a module that is
// compiled into other people's builds, and four booleans do not earn one.
func declaredCapabilities(t *testing.T) capabilities {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(packageRoot(t), "arandu.mod.toml"))
	if err != nil {
		t.Fatalf("reading arandu.mod.toml: %v", err)
	}

	var declared capabilities
	seen := map[string]bool{}
	inside := false

	for _, line := range strings.Split(string(body), "\n") {
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inside = line == "[permissions]"
			continue
		}
		if !inside {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		seen[key] = true
		switch key {
		case "network":
			declared.network = value == "true"
		case "filesystem":
			declared.filesystem = value == "true"
		case "exec":
			declared.exec = value == "true"
		case "migrations":
			declared.migrations = value == "true"
		}
	}

	for _, required := range []string{"network", "filesystem", "exec", "migrations"} {
		if !seen[required] {
			t.Errorf("arandu.mod.toml leaves %s undeclared under [permissions]; silence reads as false to whoever installs this, and a capability nobody declared is one nobody agreed to",
				required)
		}
	}
	return declared
}

// usedCapabilities is what the code actually does, by syntax.
//
// It reads calls rather than imports, for the reason the manifest gives: this
// package imports net/http for a method constant and a request type, and an
// import says nothing about whether anything leaves the process.
func usedCapabilities(files []parsedGoFile) capabilities {
	var used capabilities

	for _, source := range files {
		if strings.Contains(source.path, "migrations/") {
			used.migrations = true
		}
		for _, imported := range source.file.Imports {
			switch strings.Trim(imported.Path.Value, `"`) {
			case "os/exec":
				used.exec = true
			case "net/smtp", "net/rpc":
				used.network = true
			case "io/ioutil":
				used.filesystem = true
			}
		}

		standard := standardImports(source.file)
		ast.Inspect(source.file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CallExpr:
				switch name := qualifiedName(node, standard); {
				case name == "http.Get", name == "http.Post", name == "http.Head",
					name == "http.PostForm", name == "http.NewRequest",
					name == "http.NewRequestWithContext",
					name == "net.Dial", name == "net.DialTimeout",
					strings.HasSuffix(name, ".Do"):
					used.network = true

				case name == "os.Open", name == "os.OpenFile", name == "os.Create",
					name == "os.ReadFile", name == "os.WriteFile", name == "os.Remove",
					name == "os.RemoveAll", name == "os.Mkdir", name == "os.MkdirAll",
					name == "os.Rename", name == "os.ReadDir",
					name == "filepath.Walk", name == "filepath.WalkDir":
					used.filesystem = true

				case name == "exec.Command", name == "exec.CommandContext", name == "syscall.Exec":
					used.exec = true
				}
			case *ast.FuncDecl:
				if node.Recv != nil && node.Name.Name == "Migrations" {
					used.migrations = true
				}
			}
			return true
		})
	}
	return used
}

// standardImports maps the local name of every standard library import to the
// package's own name, so an aliased import is recognised by what it is.
//
// Only the standard library, because the names matched above are its names. A
// third-party package whose last element is "http" is not net/http, and reading
// it as one reports a router helper as an outbound call.
func standardImports(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imported := range file.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
			continue
		}

		name := path
		if slash := strings.LastIndexByte(path, '/'); slash >= 0 {
			name = path[slash+1:]
		}
		local := name
		if imported.Name != nil {
			local = imported.Name.Name
		}
		out[local] = name
	}
	return out
}

// qualifiedName is a call as "package.Function" where the receiver is an
// imported package, and ".Method" where it is a value.
func qualifiedName(call *ast.CallExpr, standard map[string]string) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		if identifier, ok := call.Fun.(*ast.Ident); ok {
			return identifier.Name
		}
		return ""
	}
	owner, ok := selector.X.(*ast.Ident)
	if !ok {
		return "." + selector.Sel.Name
	}
	if name, imported := standard[owner.Name]; imported {
		return name + "." + selector.Sel.Name
	}
	return owner.Name + "." + selector.Sel.Name
}
