package unit_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	ticket "github.com/hyz-is/arandu-ticket"
)

// placeholders are the :name tokens of a sentence.
var placeholders = regexp.MustCompile(`:[A-Za-z][A-Za-z0-9_]*`)

func TestEveryShippedLocaleSaysTheSameThings(t *testing.T) {
	t.Parallel()

	locales := ticket.Locales()
	if !slices.Contains(locales, ticket.DefaultLocale) || !slices.Contains(locales, ticket.FallbackLocale) {
		t.Fatalf("the package ships %v; it has to ship %s, which it speaks, and %s, which answers what a locale lacks", locales, ticket.DefaultLocale, ticket.FallbackLocale)
	}
	base := ticket.Lines(ticket.FallbackLocale)
	if len(base) < 80 {
		t.Fatalf("the English catalogue holds %d sentences", len(base))
	}
	for _, locale := range locales {
		lines := ticket.Lines(locale)
		for key, line := range base {
			other, ok := lines[key]
			if !ok {
				t.Errorf("%s lacks %s", locale, key)
				continue
			}
			want, got := placeholders.FindAllString(line, -1), placeholders.FindAllString(other, -1)
			sort.Strings(want)
			sort.Strings(got)
			if !slices.Equal(want, got) {
				t.Errorf("%s says %s with the placeholders %v, and English with %v", locale, key, got, want)
			}
			if strings.TrimSpace(other) == "" {
				t.Errorf("%s says nothing for %s", locale, key)
			}
		}
		for key := range lines {
			if _, ok := base[key]; !ok {
				t.Errorf("%s has %s, which English lacks", locale, key)
			}
		}
	}
}

// keyPatterns are the spellings a sentence is asked for by, with the group the
// key lives in when the code does not write it.
var keyPatterns = []struct {
	re     *regexp.Regexp
	prefix string
}{
	{regexp.MustCompile(`\.T\("([a-z_.]+)"\)`), ""},
	{regexp.MustCompile(`\.line\("([a-z_.]+)"`), ""},
	{regexp.MustCompile(`\.screen\(ctx, who, "([a-z_.]+)"`), ""},
	{regexp.MustCompile(`\.message\("([a-z_]+)"`), "validation."},
	{regexp.MustCompile(`invalid\("[a-z_]+", "([a-z_]+)"`), "validation."},
}

// dynamicGroups are read by a key built at run time, from a status, a priority,
// an origin, a role, an event or a field.
var dynamicGroups = map[string]bool{"status": true, "priority": true, "origin": true, "role": true, "activity": true, "field": true, "table": true}

func TestEverySentenceTheCodeAsksForIsInTheCatalogueAndNoneIsLeftOver(t *testing.T) {
	t.Parallel()

	root := packageRoot(t)
	asked := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "storage" || entry.Name() == "tests") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "Query.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, pattern := range keyPatterns {
			for _, match := range pattern.re.FindAllStringSubmatch(string(body), -1) {
				// A key written as a prefix, "validation." + name, is built at run time.
				if !strings.HasSuffix(match[1], ".") {
					asked[pattern.prefix+match[1]] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the sources: %v", err)
	}
	if len(asked) < 60 {
		t.Fatalf("found %d sentences asked for, so the patterns above read nothing", len(asked))
	}

	english := ticket.Lines(ticket.FallbackLocale)
	for key := range asked {
		if _, ok := english[ticket.TranslationGroup+"."+key]; !ok {
			t.Errorf("the code asks for %s and the catalogue has no such sentence", key)
		}
	}
	for full := range english {
		key := strings.TrimPrefix(full, ticket.TranslationGroup+".")
		group, _, _ := strings.Cut(key, ".")
		if !asked[key] && !dynamicGroups[group] {
			t.Errorf("the catalogue says %s and nothing asks for it", key)
		}
	}

	// The dynamic groups hold one sentence for each value the code can name.
	for _, status := range ticket.Statuses {
		if _, ok := english["ticket.status."+string(status)]; !ok {
			t.Errorf("no sentence for the status %s", status)
		}
	}
	for _, priority := range ticket.Priorities {
		if _, ok := english["ticket.priority."+string(priority)]; !ok {
			t.Errorf("no sentence for the priority %s", priority)
		}
	}
	for _, origin := range []ticket.Origin{ticket.OriginWeb, ticket.OriginContactForm, ticket.OriginEmail} {
		if _, ok := english["ticket.origin."+string(origin)]; !ok {
			t.Errorf("no sentence for the origin %s", origin)
		}
	}
	for _, role := range []ticket.Role{ticket.RoleCustomer, ticket.RoleTeam, ticket.RoleSystem} {
		if _, ok := english["ticket.role."+string(role)]; !ok {
			t.Errorf("no sentence for the role %s", role)
		}
	}
	for _, event := range []string{
		ticket.ActivityTicketOpened, ticket.ActivityTicketReplied, ticket.ActivityTicketStatusChanged, ticket.ActivityTicketPriorityChanged,
		ticket.ActivityTicketAssigned, ticket.ActivityTicketClosed, ticket.ActivityTicketReopened, ticket.ActivityTicketNoteAdded,
		ticket.ActivityAttachmentRead,
	} {
		key := "ticket.activity." + strings.ReplaceAll(event, ".", "_")
		line, ok := english[key]
		if !ok {
			t.Errorf("no description for the event %s", event)
		}
		// The log reads :word.word tokens in a description as placeholders of its
		// own; this one is filled before the log sees it, and says nothing else.
		if strings.Count(line, ":") != 1 || !strings.Contains(line, ":code") {
			t.Errorf("the description of %s is %q; it names the ticket with :code and uses no other colon", event, line)
		}
	}
}

func TestTheValidationMessagesAreSentencesInPortuguese(t *testing.T) {
	t.Parallel()

	lines := ticket.Lines("pt-BR")
	for key, line := range lines {
		if !strings.HasPrefix(key, "ticket.validation.") || key == "ticket.validation.summary" {
			continue
		}
		if !strings.HasSuffix(line, ".") {
			t.Errorf("%s is %q; a message to a person ends in a full stop", key, line)
		}
	}
	if got := lines["ticket.validation.required"]; got != "Preencha este campo." {
		t.Errorf("the required message is %q", got)
	}
}
