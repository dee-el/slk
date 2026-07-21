package blockkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/slack-go/slack"
)

// fixturePayload mirrors the shape of the JSON files in testdata/.
// Not all fields are populated for every fixture; that's fine —
// json.Unmarshal leaves missing fields zero-valued.
type fixturePayload struct {
	Blocks      slack.Blocks       `json:"blocks"`
	Attachments []slack.Attachment `json:"attachments"`
}

func loadFixture(t *testing.T, name string) fixturePayload {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var p fixturePayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return p
}

func makeCtx() Context {
	return Context{
		RenderText: func(s string, _ map[string]string) string { return s },
		WrapText:   func(s string, _ int) string { return s },
	}
}

func TestFixture_GitHubPR(t *testing.T) {
	p := loadFixture(t, "github_pr.json")
	blocks := Parse(p.Blocks)
	for _, w := range []int{60, 100, 140} {
		r := Render(blocks, makeCtx(), w)
		plain := ansi.Strip(strings.Join(r.Lines, "\n"))
		for _, want := range []string{"Pull Request opened", "Fix retry logic", "3 files changed"} {
			if !strings.Contains(plain, want) {
				t.Errorf("width=%d missing %q in %q", w, want, plain)
			}
		}
	}
}

func TestFixture_PagerDutyAlert(t *testing.T) {
	p := loadFixture(t, "pagerduty_alert.json")
	atts := ParseAttachments(p.Attachments)
	for _, w := range []int{60, 100, 140} {
		r := RenderLegacy(atts, makeCtx(), w)
		plain := ansi.Strip(strings.Join(r.Lines, "\n"))
		for _, want := range []string{"Service down", "checkout-svc", "SEV-2", "Datadog"} {
			if !strings.Contains(plain, want) {
				t.Errorf("width=%d missing %q in %q", w, want, plain)
			}
		}
		if !strings.Contains(plain, "█") {
			t.Errorf("width=%d missing color stripe", w)
		}
	}
}

func TestFixture_DeployApproval(t *testing.T) {
	p := loadFixture(t, "deploy_approval.json")
	blocks := Parse(p.Blocks)
	for _, w := range []int{60, 100, 140} {
		r := Render(blocks, makeCtx(), w)
		plain := ansi.Strip(strings.Join(r.Lines, "\n"))
		if !strings.Contains(plain, "Deploy v2.3.1") {
			t.Errorf("width=%d missing body: %q", w, plain)
		}
		if !strings.Contains(plain, "[ Approve ]") || !strings.Contains(plain, "[ Deny ]") {
			t.Errorf("width=%d missing buttons: %q", w, plain)
		}
		if !r.Interactive {
			t.Errorf("width=%d Interactive should be true", w)
		}
	}
}

func TestFixture_OncallHandoff(t *testing.T) {
	p := loadFixture(t, "oncall_handoff.json")
	blocks := Parse(p.Blocks)
	for _, w := range []int{60, 100, 140} {
		r := Render(blocks, makeCtx(), w)
		plain := ansi.Strip(strings.Join(r.Lines, "\n"))
		for _, want := range []string{"Weekly on-call handoff", "alice", "bob", "rotates Mondays"} {
			if !strings.Contains(plain, want) {
				t.Errorf("width=%d missing %q in %q", w, want, plain)
			}
		}
	}
}

func TestFixture_SectionWithFields(t *testing.T) {
	p := loadFixture(t, "section_with_fields.json")
	blocks := Parse(p.Blocks)
	r := Render(blocks, makeCtx(), 100)
	plain := ansi.Strip(strings.Join(r.Lines, "\n"))
	for _, want := range []string{"Build complete", "Branch", "Commit", "Duration", "abc1234"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in %q", want, plain)
		}
	}
}

func TestFixture_HeaderDividerSection(t *testing.T) {
	p := loadFixture(t, "header_divider_section.json")
	blocks := Parse(p.Blocks)
	r := Render(blocks, makeCtx(), 80)
	if r.Height < 3 {
		t.Errorf("Height = %d, want >= 3 (header, divider, body)", r.Height)
	}
	plain := ansi.Strip(strings.Join(r.Lines, "\n"))
	for _, want := range []string{"Top header", "Body text after divider"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in %q", want, plain)
		}
	}
}

func TestFixture_TableBlock(t *testing.T) {
	p := loadFixture(t, "table_block.json")
	blocks := Parse(p.Blocks)
	ctx := Context{
		UserNames: map[string]string{"U123": "alice"},
		RenderText: func(s string, userNames map[string]string) string {
			for id, name := range userNames {
				s = strings.ReplaceAll(s, "<@"+id+">", "@"+name)
			}
			return s
		},
		WrapText: func(s string, width int) string { return ansi.Wrap(s, width, "") },
	}
	for _, tc := range []struct {
		width int
		wants []string
	}{
		{width: 12, wants: []string{"Service", "API"}},
		{width: 60, wants: []string{"Service", "Status", "Owner", "API", "Healthy", "@alice", "runbook"}},
	} {
		r := Render(blocks, ctx, tc.width)
		plain := ansi.Strip(strings.Join(r.Lines, "\n"))
		for _, want := range tc.wants {
			if !strings.Contains(plain, want) {
				t.Fatalf("width=%d missing %q in %q", tc.width, want, plain)
			}
		}
		if strings.Contains(plain, "[unsupported block: table]") {
			t.Fatalf("width=%d still rendered unsupported marker: %q", tc.width, plain)
		}
		if len(r.TableRegions) != 1 {
			t.Fatalf("width=%d table regions = %d, want 1", tc.width, len(r.TableRegions))
		}
	}
}

func TestFixture_TableMixedCells(t *testing.T) {
	p := loadFixture(t, "table_mixed_cells.json")
	blocks := Parse(p.Blocks)
	ctx := makeCtx()
	ctx.MessageTS = testTableMessageTS
	r := Render(blocks, ctx, 64)
	plain := ansi.Strip(strings.Join(r.Lines, "\n"))
	for _, want := range []string{"Metric", "Count", "Rate", "Errors", "42.5", "12.5%", "Builds", "7"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in %q", want, plain)
		}
	}
	if strings.Contains(plain, "[unsupported block: table]") {
		t.Fatalf("still rendered unsupported marker: %q", plain)
	}
	if len(r.TableRegions) != 1 {
		t.Fatalf("table regions = %d, want 1", len(r.TableRegions))
	}
	key := testTableKey("mixed-table", "blocks/0")
	ctx.TableViewports = map[TableKey]TableViewportInput{key: {XOffset: 18, YOffset: 4}}
	r = Render(blocks, ctx, 18)
	plain = ansi.Strip(strings.Join(r.Lines, "\n"))
	for _, want := range []string{"12.5%", "green"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("viewport missing %q in %q", want, plain)
		}
	}
}
