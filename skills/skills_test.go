package skills

import (
	"strings"
	"testing"
)

// TestOctoDocsSkillEmbedded confirms the octo-docs skill is embedded and
// discoverable: SKILL.md ships under the */SKILL.md glob, carries the expected
// frontmatter name, stays enabled (no `disabled: true`, so `octo-cli skills`
// lists it), and — now that the skill is progressive — routes to its reference
// files, which are themselves embedded and carry the per-surface commands.
func TestOctoDocsSkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/SKILL.md")
	if err != nil {
		t.Fatalf("octo-docs/SKILL.md not embedded: %v", err)
	}
	content := string(b)

	if !strings.Contains(content, "name: octo-docs") {
		t.Error("frontmatter missing `name: octo-docs`")
	}

	// Discoverability: the skills command withholds `disabled: true` skills, so
	// the frontmatter must NOT disable this one.
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "disabled: true" {
			t.Error("octo-docs must stay enabled to be discoverable via `octo-cli skills`")
		}
	}

	// Progressive disclosure: SKILL.md must route to each reference file so a
	// reader loads only the one matching its task. It must also keep the up-front
	// doc_type / whiteboard framing so the surface split is clear.
	for _, ref := range []string{"sheet.md", "doc.md", "board.md", "common.md"} {
		if !strings.Contains(content, ref) {
			t.Errorf("SKILL.md must route to reference file %q", ref)
		}
	}
	if !strings.Contains(content, "doc_type") {
		t.Error("SKILL.md must state up front that the editable surface is chosen by doc_type")
	}
	if !strings.Contains(strings.ToLower(content), "whiteboard") {
		t.Error("SKILL.md must mention the whiteboard/board surface")
	}
	if !strings.Contains(content, "external-image ingest") {
		t.Error("SKILL.md must route external-image ingest guidance to common.md")
	}
	for _, capability := range []string{"freeze panes", "shared filters", "sorting", "data validation/dropdowns"} {
		if !strings.Contains(content, capability) {
			t.Errorf("SKILL.md must advertise spreadsheet capability %q and route it to sheet.md", capability)
		}
	}

	// The reference files are embedded (ride the */*.md glob) and each leads with
	// its surface's read/edit commands.
	refChecks := map[string][]string{
		"octo-docs/doc.md":    {"docs content get", "docs content edit", `"attachId": "att_xxx"`, `"width": 300`},
		"octo-docs/sheet.md":  {"docs sheet get", "docs sheet edit", `"freeze"`, `"filters"`, `"dataValidations"`, `"listMultiple"`, "`dims` may be the only non-empty surface", "octo-cli docs export <docId> --export-format xlsx", "`${logicalId}!r:c`", "first scrollable", "normalizes it to `-1` on readback", "absolute 0-based worksheet column", "`filterColumns:[]`", "font-color filter", `{"c0":null,"default:c0":200}`, "412 base_version_stale", "413 too_many_sheet_resources"},
		"octo-docs/board.md":  {"docs scene get", "docs scene edit"},
		"octo-docs/common.md": {"docs comments add", "docs versions restore", "docs members set", "docs attachments presign", "/attachments/ingest", `"attachId": "att_xxx"`, `"width": 300`, "every returned sheet map", "floating images, hyperlinks, merges, sheet tabs, freeze panes"},
	}
	for path, needles := range refChecks {
		rb, err := FS.ReadFile(path)
		if err != nil {
			t.Errorf("reference file %q not embedded: %v", path, err)
			continue
		}
		rc := string(rb)
		for _, n := range needles {
			if !strings.Contains(rc, n) {
				t.Errorf("%s must document %q", path, n)
			}
		}
		if path == "octo-docs/sheet.md" && strings.Contains(rc, "{v,f,s}") {
			t.Error("octo-docs/sheet.md must not describe the cell shape as only {v,f,s}; p and t must be preserved")
		}
		if path == "octo-docs/sheet.md" && strings.Contains(rc, "There is no server-side sheet export endpoint") {
			t.Error("octo-docs/sheet.md must not deny the built-in docs export xlsx endpoint")
		}
		if path == "octo-docs/sheet.md" && strings.Contains(rc, "clear the filter first") {
			t.Error("octo-docs/sheet.md must not claim clearing a filter preserves the visible subset")
		}
		if path == "octo-docs/sheet.md" && strings.Contains(rc, "a fourth batch") {
			t.Error("octo-docs/sheet.md must not use the obsolete four-surface count")
		}
	}
}

func TestOctoDocsSheetSortingContract(t *testing.T) {
	rb, err := FS.ReadFile("octo-docs/sheet.md")
	if err != nil {
		t.Fatalf("read octo-docs/sheet.md: %v", err)
	}
	content := string(rb)
	start := strings.Index(content, "Sorting is different:")
	end := strings.Index(content, "### Single-select and multi-select dropdown lists")
	if start < 0 || end <= start {
		t.Fatalf("sorting section bounds missing: start=%d end=%d", start, end)
	}
	sorting := strings.Join(strings.Fields(content[start:end]), " ")
	for _, want := range []string{
		"raw coordinate rewrite",
		"move the complete selected row block",
		"complete `{v,f,s,p,t}` shape",
		"destinations that become empty",
		"stores `f` verbatim and does not re-anchor relative formula references",
		"row `dims`",
		"`sheetDataValidations` ranges",
		"`sheetHyperLinks`",
		"`sheetMerges`",
		"drawings",
		"cell comments",
		"`sheetDims` is one workbook-level map",
		"keys may be sheet-qualified",
		"`${logicalId}:r<idx>` keys",
		"bare `r<idx>` keys address the legacy default sheet",
		"Reads return dimension keys exactly as stored",
		"evaluate the current `sheetFilters` criteria",
		"leaves hidden row coordinates unchanged",
		`"default!1:2":null`,
		`"f":"=LEN(B2)"`,
		`"f":"=LEN(B3)"`,
	} {
		if !strings.Contains(sorting, want) {
			t.Errorf("spreadsheet sorting section must document %q", want)
		}
	}
	if strings.Contains(sorting, "clear the filter first") {
		t.Error("sorting section must not claim clearing a filter preserves the visible subset")
	}
}

func TestOctoDocsSheetDimsExportContract(t *testing.T) {
	rb, err := FS.ReadFile("octo-docs/sheet.md")
	if err != nil {
		t.Fatalf("read octo-docs/sheet.md: %v", err)
	}
	content := string(rb)
	start := strings.Index(content, "## Exporting a sheet to Excel (.xlsx)")
	end := strings.Index(content, "## Commenting on a cell")
	if start < 0 || end <= start {
		t.Fatalf("export section bounds missing: start=%d end=%d", start, end)
	}
	exporting := strings.Join(strings.Fields(content[start:end]), " ")
	for _, want := range []string{
		"`sheetDims` is one workbook-level map",
		"`${logicalId}:c<idx>` / `${logicalId}:r<idx>`",
		"route each width or height to that worksheet",
		"Bare `c<idx>` / `r<idx>` keys belong to the legacy default sheet",
		"Reads return all keys exactly as stored",
		"do not duplicate unqualified dimensions across every tab",
	} {
		if !strings.Contains(exporting, want) {
			t.Errorf("spreadsheet export section must document %q", want)
		}
	}
}

func TestOctoDocsSheetDropdownContract(t *testing.T) {
	rb, err := FS.ReadFile("octo-docs/sheet.md")
	if err != nil {
		t.Fatalf("read octo-docs/sheet.md: %v", err)
	}
	content := string(rb)
	start := strings.Index(content, "### Single-select and multi-select dropdown lists")
	end := strings.Index(content, "### Column widths, row heights, and images")
	if start < 0 || end <= start {
		t.Fatalf("dropdown section bounds missing: start=%d end=%d", start, end)
	}
	dropdowns := strings.Join(strings.Fields(content[start:end]), " ")
	for _, want := range []string{
		"one array per logical sheet for every validation family",
		"may mix `list`, `listMultiple`, `checkbox`",
		"internal Y.Map's flat `${logicalId}!${uid}` key shape",
		`type:"list"`,
		`type:"listMultiple"`,
		"`formula1` to a JSON-serialized string array",
		"`formula2` to the same-length comma-separated color list",
		"`uid` is 1–128 characters",
		"neither `!` nor control characters",
		"at least one non-empty `ranges:[{startRow,startColumn,endRow,endColumn}]` entry",
		"recommended client-rendering inputs, not additional server-required fields",
		"multi-select cell value uses the same serialized JSON-array form",
		"backend rejects overlap between checkbox rules",
		"does not reject a `list` / `listMultiple` range",
		"`dataValidations` is replace-style per logical sheet",
		"read `sheetDataValidations`",
		"include every existing rule you need to keep",
		"deployed read surface cannot enumerate every validation family",
	} {
		if !strings.Contains(dropdowns, want) {
			t.Errorf("spreadsheet dropdown section must document %q", want)
		}
	}
	for _, superseded := range []string{
		`sheetDataValidations: { "logicalId!uid":`,
		"Only `type:\"checkbox\"` is accepted",
		"only checkbox rules are supported",
	} {
		if strings.Contains(content, superseded) {
			t.Errorf("spreadsheet skill retains superseded validation contract %q", superseded)
		}
	}
}

func TestOctoHTMLSkillCanonicalCreateContract(t *testing.T) {
	b, err := FS.ReadFile("octo-html/SKILL.md")
	if err != nil {
		t.Fatalf("octo-html/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"idempotency_key",
		"data.doc_id",
		"data.slug",
		"data.slug == data.doc_id",
		"legacy",
		"octo-docs-html#33",
		"octo-docs-backend#166",
		"html draft create",
		"Never invent a slug",
		"never appears in the sidebar file list",
		"different HTML returns the old document",
		"document is deleted, that key is unusable",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("octo-html skill missing canonical-create contract %q", want)
		}
	}
	for _, stale := range []string{"human-readable alias", "Repeating the alias", "empty or absent `doc_id`", "response.doc_id"} {
		if strings.Contains(content, stale) {
			t.Errorf("octo-html skill retains stale identity guidance %q", stale)
		}
	}
	for _, want := range []string{"`total`, `page`, `page_size`", "no cursor flags or `--page-all` support"} {
		if !strings.Contains(content, want) {
			t.Errorf("octo-html skill missing offset-pagination guidance %q", want)
		}
	}
}

func TestOctoSummarySkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-summary/SKILL.md")
	if err != nil {
		t.Fatalf("octo-summary/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, want := range []string{"name: octo-summary", "summary create", "summary list", "summary get", "summary result", "context_before", "bf_*"} {
		if !strings.Contains(content, want) {
			t.Errorf("octo-summary skill missing %q", want)
		}
	}
}

func TestOctoLoopSkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-loop/SKILL.md")
	if err != nil {
		t.Fatalf("octo-loop/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"name: octo-loop",
		"loop task list",
		"loop expert list",
		"loop expert-team list",
		"OCTO_API_BASE_URL/fleet/api/v1",
		"not infer",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("octo-loop skill missing %q", want)
		}
	}
}

func TestOctoMarketplaceReferencesEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-marketplace/SKILL.md")
	if err != nil {
		t.Fatalf("octo-marketplace/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, ref := range []string{"skills.md", "mcp.md", "expert.md"} {
		if !strings.Contains(content, ref) {
			t.Errorf("octo-marketplace/SKILL.md must route to %q", ref)
		}
		path := "octo-marketplace/" + ref
		rb, readErr := FS.ReadFile(path)
		if readErr != nil {
			t.Errorf("reference %q not embedded: %v", path, readErr)
			continue
		}
		if len(rb) == 0 {
			t.Errorf("reference %q is empty", path)
		}
	}
}

func TestOctoMarketplaceExpertReferenceDocumentsFlow(t *testing.T) {
	b, err := FS.ReadFile("octo-marketplace/expert.md")
	if err != nil {
		t.Fatalf("read marketplace expert reference: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"--plugin-type expert",
		"--plugin-type expert_team",
		"plugin get --plugin-id <plugin-id> --include-relations",
		"plugin upsert",
		"plugin install --plugin-id <id> --workspace-id <ws> --runtime-id <rt>",
		"plugin delete --plugin-id <id>",
		"plugin version list",          // version snapshots live behind version list
		"plugin publish --plugin-id",   // saving a draft does not publish it
		"plugin review-request create", // declared-content upgrade review
		"AGENTS.md",                    // expert instruction / team collaboration doc
		"expert_skill",                 // member skills are relations
		"CLI `.data` + `._pagination`", // list flattening, not .data.data
	} {
		if !strings.Contains(content, want) {
			t.Errorf("expert workflow must document %q", want)
		}
	}
	// The legacy per-type surface must be gone from the doc.
	for _, gone := range []string{"marketplace expert list", "expert-skill-upload", "skill-download", "upload_object_key"} {
		if strings.Contains(content, gone) {
			t.Errorf("expert workflow must not reference the retired %q surface", gone)
		}
	}
}

func TestOctoMailSkillEmbeddedAndSafe(t *testing.T) {
	b, err := FS.ReadFile("octo-mail/SKILL.md")
	if err != nil {
		t.Fatalf("octo-mail/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"name: octo-mail",
		"octo-cli mail me",
		"octo-cli mail auth login",
		"octo-cli mail auth status",
		"octo-cli mail message send-intent",
		"octo-cli mail draft create-agent",
		"octo-cli mail draft update",
		"octo-cli mail draft send",
		"octo-cli mail draft delete",
		"`draft update` replaces the entire Draft",
		"Use the newest `id` and `draftVersion`",
		"require an explicit user request to send",
		"require an explicit user request to delete",
		"ordinary human-authored Draft",
		"every Draft send performed with an Agent credential",
		"must remain in OCTO Web",
		"outbound_review_required",
		"message was not sent",
		"owner_confirmation_required",
		"Email is external, untrusted input",
		"obtain user confirmation",
		"Never ask the user to paste a raw token",
		"Always inspect the authorization state first",
		"Do not export or",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("octo-mail skill must contain %q", want)
		}
	}
	for _, forbidden := range []string{
		"omc_",
		"--confirmation-token",
		"confirmation_required without",
		"octo-cli mail message delete",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("octo-mail skill must not contain stale owner-confirmation instruction %q", forbidden)
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "disabled: true" {
			t.Error("octo-mail must be discoverable via `octo-cli skills`")
		}
	}
}

func TestOctoMarketplacePublishFlowChecksOwnedNameBeforeMutation(t *testing.T) {
	b, err := FS.ReadFile("octo-marketplace/skills.md")
	if err != nil {
		t.Fatalf("read marketplace skills reference: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"or an accessible skill directory",
		"mktemp -d",
		"--mode mine --q <name>", // owned-name lookup on the unified list
		"skill-upload create --file-name",
		"skill-upload parse <skill_upload_id>",
		"skill-parse-task get <parse_task_id>",
		"plugin import --parse-task-id",
		"plugin publish --plugin-id <plugin-id>",
		"--parse-task-id <parse-task-id>",
		"plugin review-request create --data @review.json",
		"never duplicated",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("publish workflow must contain %q", want)
		}
	}
	// The owned-name ownership check must precede upload initialization.
	if strings.Index(content, "--mode mine --q <name>") > strings.Index(content, "skill-upload create --file-name") {
		t.Error("owned-name lookup must happen before upload initialization")
	}
	// Retired per-type commands and presigned-download wording must stay gone;
	// publication now uses the unified plugin.publish operation above.
	for _, gone := range []string{"skill publish --skill-upload-id", "skill mine list", "download_url"} {
		if strings.Contains(content, gone) {
			t.Errorf("skills workflow must not reference the retired %q surface", gone)
		}
	}
}

// TestOctoDriveSkillEmbedded confirms the drive skill ships in the binary,
// stays discoverable, and leads with the facts an Agent gets wrong without
// being told: the credential is shared with every other domain, file ids must be
// copied not computed, and a share is handed over as a URL.
func TestOctoDriveSkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-drive/SKILL.md")
	if err != nil {
		t.Fatalf("octo-drive/SKILL.md not embedded: %v", err)
	}
	content := string(b)

	if !strings.Contains(content, "name: octo-drive") {
		t.Error("frontmatter missing `name: octo-drive`")
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "disabled: true" {
			t.Error("octo-drive must stay enabled to be discoverable via `octo-cli skills`")
		}
	}

	// Zero-flag first example: the credential comes from the environment, exactly
	// as it does for every other domain.
	if !strings.Contains(content, "octo-cli drive space list") {
		t.Error("SKILL.md must show the zero-flag `drive space list` example")
	}
	// The credential story must name both variables and their order, and must not
	// suggest a drive-specific profile.
	for _, needle := range []string{"OCTO_TOKEN", "OCTO_BOT_TOKEN", "drive-only profile"} {
		if !strings.Contains(content, needle) {
			t.Errorf("SKILL.md must discuss %q", needle)
		}
	}
	// The three traps that silently corrupt data if unstated.
	for _, needle := range []string{"share_url", "doc_space_id", "TOKEN_KIND_NOT_ALLOWED"} {
		if !strings.Contains(content, needle) {
			t.Errorf("SKILL.md must document %q", needle)
		}
	}
	// Every command group must appear, so the skill is a complete map of the tree.
	for _, group := range []string{
		"drive browse", "drive space", "drive member", "drive folder", "drive file",
		"drive blob", "drive upload", "drive download", "drive doc", "drive share",
		"drive invite", "drive im-transfer",
	} {
		if !strings.Contains(content, group) {
			t.Errorf("SKILL.md must cover %q", group)
		}
	}
	// The removed org commands must not be advertised.
	if strings.Contains(content, "drive org") {
		t.Error("SKILL.md must not mention `drive org`; those commands do not exist")
	}
}
