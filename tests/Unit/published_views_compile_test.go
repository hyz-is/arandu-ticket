package unit_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
)

// viewCompiler is the command line whose view compiler the published views are
// held against, at the release an application builds them with.
//
// It is a version and never "latest", so this test changes its answer when this
// line changes and never because somebody published something. Moving it is
// how a stricter compiler is taken on: raise it, run the suite, and fix the
// view it refuses here rather than in every application that publishes it.
const viewCompiler = "github.com/arandu-io/aru@v0.74.0"

// TestEveryPublishedViewCompiles publishes the views into a project of their
// own, builds them with the view compiler an application runs, and compiles
// what it wrote.
//
// Nothing else in this repository reads the markup. The sources open with a
// build tag, so go build, go vet and go test never look past it, and the view
// compiler refuses what the Go compiler would accept -- a value written into an
// address behind text it cannot read, a value in a position no escape covers.
// A view it refuses is a view that stops the build of every application that
// publishes it, and without this test the first place to say so is somebody
// else's terminal.
//
// The project is laid out as an application's is once it has published: each
// file where the publication writes it, and a go.mod that resolves this package
// to the checkout under test. What the view compiler writes then goes through
// the Go compiler, which is what catches a field the markup names and the page
// data does not have.
//
// It does not skip. A gate that steps aside when its input is missing is how
// the views stopped compiling with nothing here saying so.
func TestEveryPublishedViewCompiles(t *testing.T) {
	root := packageRoot(t)
	project := t.TempDir()

	publications, err := foundation.Publications(module(t))
	if err != nil {
		t.Fatalf("reading what the module publishes: %v", err)
	}
	var published int
	for _, publication := range publications {
		from := publication.From
		if from == "" {
			from = "."
		}
		err := fs.WalkDir(publication.Files, from, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative := name
			if from != "." {
				relative = strings.TrimPrefix(strings.TrimPrefix(name, from), "/")
			}
			body, err := fs.ReadFile(publication.Files, name)
			if err != nil {
				return err
			}
			target := filepath.Join(project, filepath.FromSlash(path.Join(publication.To, relative)))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			published++
			return os.WriteFile(target, body, 0o644)
		})
		if err != nil {
			t.Fatalf("publishing the %s files: %v", publication.Tag, err)
		}
	}
	if published == 0 {
		t.Fatal("the module publishes no file, so this gate would compile nothing")
	}

	// The application's go.mod names this package and points it at the
	// checkout, so the views compile against the code beside them rather than
	// against a release. The sums come from here: the application needs no
	// module this package does not already require.
	goMod := readReleaseFile(t, root, "go.mod")
	language := captureReleaseValue(t, goMod, `(?m)^go ([0-9.]+)$`, "go directive in go.mod")
	modulePath := captureReleaseValue(t, goMod, `(?m)^module (\S+)$`, "module path in go.mod")
	manifest := "module example.com/application\n\n" +
		"go " + language + "\n\n" +
		"require " + modulePath + " v0.0.0-00010101000000-000000000000\n\n" +
		"replace " + modulePath + " => " + strconv.Quote(root) + "\n"
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.sum"), []byte(readReleaseFile(t, root, "go.sum")), 0o644); err != nil {
		t.Fatal(err)
	}

	inProject(t, project, "the published views do not compile with "+viewCompiler+", so no application that publishes them can build",
		"go", "run", viewCompiler, "view:build")
	inProject(t, project, "the views "+viewCompiler+" compiled do not build against this package",
		"go", "build", "./...")

	// Compiling is not drawing. Each screen is rendered once, with a row of
	// every kind and every action switched on, under a layout that draws the
	// content section alone -- so a page that compiles and then fails on its
	// own data, or draws a key instead of a sentence, fails here.
	probe := filepath.Join(project, "renderprobe")
	if err := os.MkdirAll(probe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probe, "main.go"), []byte(renderProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	inProject(t, project, "a published view does not render the page data its handler hands it",
		"go", "run", "./renderprobe")
}

// renderProbe renders every screen with sample data and checks what it drew.
const renderProbe = `package main

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"

	"github.com/arandu-io/hesape/view"
	"github.com/arandu-io/kyse/components"
	ticket "github.com/hyz-is/arandu-ticket"

	_ "example.com/application/storage/framework/views/modules/ticket"
	_ "example.com/application/storage/framework/views/modules/ticket/admin"
)

func main() {
	view.RegisterLayout("layouts.app", func(w io.Writer, _ any, sections map[string]func(io.Writer) error) error {
		return sections["content"](w)
	})
	sc := ticket.Screen{
		Labels: ticket.Labels{Locale: "pt-BR"},
		URLs:   ticket.NewURLs("/painel/chamados", "/admin/chamados", ticket.PortuguesePaths),
	}
	row := ticket.TicketRow{ID: "t1", Code: "#000123", Subject: "Câmera offline", Category: "Câmeras", Status: "Aguardando cliente",
		StatusKey: "waiting_customer", Priority: "Urgente", PriorityKey: "urgent", Requester: "Ana Souza", RequesterEmail: "ana@acme.test",
		Assignee: "Rita", AssigneeID: "team-1", Space: "acme", Origin: "Painel", Created: "26/10/2026 12:00", Updated: "26/10/2026 13:30",
		URL: "/painel/chamados/t1"}
	teamRow := row
	teamRow.URL = "/admin/chamados/t1"
	messages := []ticket.MessageRow{
		{ID: "m1", Author: "Ana Souza", Role: "Cliente", RoleKey: "customer", When: "26/10/2026 12:00", WhenISO: "2026-10-26T15:00:00Z",
			Body: template.HTML("<p>Parou ontem.</p>"), Files: []ticket.FileRow{{Name: "tela.png", Type: "image/png", Size: "40 B", URL: "/painel/chamados/t1/anexos/f1"}}},
		{ID: "m2", Author: "Rita", Role: "Equipe", RoleKey: "team", When: "26/10/2026 13:30", WhenISO: "2026-10-26T16:30:00Z", Body: template.HTML("<p>Pode reiniciar?</p>")},
	}
	note := ticket.MessageRow{ID: "m3", Author: "Rita", Role: "Equipe", RoleKey: "team", When: "26/10/2026 13:31", WhenISO: "2026-10-26T16:31:00Z",
		Internal: true, Body: template.HTML("<p>Cliente já estornado.</p>")}
	options := func(values ...string) []components.SelectOption {
		out := make([]components.SelectOption, 0, len(values))
		for _, v := range values {
			out = append(out, components.SelectOption{Value: v, Label: v})
		}
		return out
	}

	pages := []struct {
		name string
		data any
		want []string
		not  []string
	}{
		{ticket.ViewIndex, ticket.IndexPageData{Screen: sc, Rows: []ticket.TicketRow{row}, URL: "/painel/chamados", NewURL: "/painel/chamados/novo",
			CanCreate: true, CurrentPage: 1, Pages: 1, Total: 1, PerPage: 25, StatusOptions: options("open", "closed"),
			Pager: components.Paginator(nil)},
			[]string{"Chamados", "#000123", "Câmera offline", "Aguardando cliente", "/painel/chamados/t1", "Novo chamado", "/painel/chamados/novo",
				"Buscar em Chamados", "Situação"},
			[]string{"Urgente", "Rita", "Painel"}},
		{ticket.ViewIndex, ticket.IndexPageData{Screen: sc, URL: "/painel/chamados", CurrentPage: 1, Pages: 1, PerPage: 25},
			[]string{"Nenhum chamado por aqui", "Quando você abrir um chamado"}, []string{"Novo chamado"}},
		{ticket.ViewNew, ticket.NewPageData{Screen: sc, Action: "/painel/chamados", BackURL: "/painel/chamados", Files: true, MaxFiles: 5, MaxMB: "5",
			Accept: "image/png,application/pdf"},
			[]string{"Novo chamado", "Assunto", "Categoria", "Mensagem", "enctype=\"multipart/form-data\"", "type=\"file\"", "image/png,application/pdf",
				"Até 5 arquivos de 5 MB cada", "Abrir chamado", "Cancelar", "_token"}, nil},
		{ticket.ViewNew, ticket.NewPageData{Screen: sc, Action: "/painel/chamados", BackURL: "/painel/chamados"},
			[]string{"Novo chamado"}, []string{"type=\"file\""}},
		{ticket.ViewShow, ticket.ShowPageData{Screen: sc, Ticket: row, Messages: messages, BackURL: "/painel/chamados",
			ReplyURL: "/painel/chamados/t1/respostas", CloseURL: "/painel/chamados/t1/encerramento", ReopenURL: "/painel/chamados/t1/reabertura",
			CanReply: true, CanClose: true, Files: true, MaxFiles: 5, MaxMB: "5", Accept: "image/png"},
			[]string{"#000123", "Câmera offline", "Parou ontem.", "Pode reiniciar?", "tela.png", "/painel/chamados/t1/anexos/f1", "Enviar resposta",
				"/painel/chamados/t1/respostas", "Encerrar chamado", "/painel/chamados/t1/encerramento", "datetime=\"2026-10-26T15:00:00Z\"", "Conversa"},
			[]string{"Nota interna", "Reabrir chamado", "Urgente", "Rita</dd>"}},
		{ticket.ViewShow, ticket.ShowPageData{Screen: sc, Ticket: row, Messages: messages, BackURL: "/painel/chamados",
			ReopenURL: "/painel/chamados/t1/reabertura", CanReopen: true},
			[]string{"Reabrir chamado", "/painel/chamados/t1/reabertura"}, []string{"Enviar resposta", "Encerrar chamado"}},
		{ticket.ViewTeamIndex, ticket.TeamIndexPageData{Screen: sc, Rows: []ticket.TicketRow{teamRow}, URL: "/admin/chamados",
			CurrentPage: 1, Pages: 3, Total: 60, StatusOptions: options("open"), PriorityOptions: options("urgent"), AssigneeOptions: options("none")},
			[]string{"Chamados dos clientes", "#000123", "Ana Souza", "acme", "Urgente", "Aguardando cliente", "Rita", "/admin/chamados/t1",
				"Buscar por número, assunto, nome ou e-mail", "Prioridade", "Responsável"}, nil},
		{ticket.ViewTeamShow, ticket.TeamShowPageData{Screen: sc, Ticket: row, Messages: append(append([]ticket.MessageRow(nil), messages...), note),
			BackURL: "/admin/chamados", ReplyURL: "/admin/chamados/t1/respostas", NoteURL: "/admin/chamados/t1/notas",
			StatusURL: "/admin/chamados/t1/situacao", PriorityURL: "/admin/chamados/t1/prioridade", AssignURL: "/admin/chamados/t1/responsavel",
			CanReply: true, CanNote: true, CanManage: true, CanAssign: true, StatusOptions: options("open", "closed"),
			PriorityOptions: options("low", "urgent"), AssigneeOptions: options("team-1"), Files: true, MaxFiles: 5, MaxMB: "5", Accept: "image/png"},
			[]string{"#000123 · acme", "Nota interna", "Cliente já estornado.", "Responder ao cliente", "Salvar nota", "/admin/chamados/t1/notas",
				"/admin/chamados/t1/situacao", "/admin/chamados/t1/prioridade", "/admin/chamados/t1/responsavel", "ana@acme.test", "Solicitante", "Espaço"}, nil},
		{ticket.ViewTeamShow, ticket.TeamShowPageData{Screen: sc, Ticket: row, Messages: messages, BackURL: "/admin/chamados"},
			[]string{"#000123 · acme"}, []string{"Responder ao cliente", "Salvar nota", "Salvar"}},
	}

	inline := regexp.MustCompile(` + "`" + `(?i)(<script|\sstyle=|\son[a-z]+=|javascript:)` + "`" + `)
	failed := false
	for _, p := range pages {
		rec := httptest.NewRecorder()
		if err := view.NewRenderer().Render(context.Background(), rec, 200, p.name, p.data); err != nil {
			fmt.Printf("%s: %v\n", p.name, err)
			failed = true
			continue
		}
		body := rec.Body.String()
		for _, want := range p.want {
			if !strings.Contains(body, want) {
				fmt.Printf("%s: the page does not say %q\n", p.name, want)
				failed = true
			}
		}
		for _, not := range p.not {
			if strings.Contains(body, not) {
				fmt.Printf("%s: the page says %q, which this data must not draw\n", p.name, not)
				failed = true
			}
		}
		if strings.Contains(body, "ticket.screen.") || strings.Contains(body, "ticket.field.") || strings.Contains(body, "ticket.control.") {
			fmt.Printf("%s: the page draws a catalogue key instead of its sentence\n", p.name)
			failed = true
		}
		// The content security policy is 'self': no inline script, no inline
		// style, no event attribute and no javascript: address.
		if found := inline.FindString(body); found != "" {
			fmt.Printf("%s: the page carries %q, which a policy of 'self' refuses\n", p.name, found)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
`

// inProject runs one command in the scratch project, outside any workspace,
// and fails with what it printed.
//
// -mod=mod lets the go command fill in the requirements the application's
// go.mod leaves to this package's, which is what an application's own `go mod
// tidy` would have written.
func inProject(t *testing.T, dir, failure string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS="+strings.TrimSpace(os.Getenv("GOFLAGS")+" -mod=mod"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s:\n$ %s %s\n%s", failure, name, strings.Join(args, " "), output)
	}
}
