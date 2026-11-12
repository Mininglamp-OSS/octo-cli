package permission

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeReaderFailClosedSaturationAndImmutabilityFences(t *testing.T) {
	var (
		readSeq   int
		headerRef = "header_1"
		msgID     = "100"
		seq       = "100"
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		refs := req["references"].([]any)
		ref := refs[0].(map[string]any)["delivery_reference"].(string)
		purpose := req["purpose"].(string)
		readSeq++
		readID := NewID()
		imNonce := NewID()
		sqlNonce := NewID()
		header := map[string]any{
			"header_version":         1,
			"header_reference":       headerRef,
			"message_id":             msgID,
			"source":                 map[string]any{"kind": "group", "id": "group"},
			"parent_group":           nil,
			"space_id":               "space",
			"author_uid":             "alice",
			"author_kind":            "human",
			"recipient_bot_uid":      "bot",
			"native_message_version": "v1",
			"payload_sha256":         strings.Repeat("a", 64),
			"delivery":               map[string]any{"stream_id": "stream", "stream_incarnation": strings.Repeat("1", 32), "sequence": seq},
		}
		profile := "group_" + purpose + "_v1"
		var shared struct {
			Profiles map[string][]string `json:"predicate_profiles"`
		}
		_ = json.Unmarshal(nativeSharedContract, &shared)
		predicates := map[string]any{}
		for _, atom := range shared.Profiles[profile] {
			predicates[atom] = true
		}
		reply := map[string]any{
			"schema_version":  1,
			"profile_version": 1,
			"key":             map[string]any{"origin": "https://" + r.Host, "bot_uid": "bot", "space_id": "space"},
			"request_id":      req["request_id"],
			"read_id":         readID,
			"observed_at":     time.Now().UTC().Format(time.RFC3339Nano),
			"authorities": []any{
				map[string]any{"authority_id": "im", "kind": "im", "incarnation": strings.Repeat("3", 32), "position": "1", "read_nonce": imNonce, "consistency": "ack_before_read_start", "started_at": "2026-01-01T00:00:00Z", "completed_at": "2026-01-01T00:00:00Z"},
				map[string]any{"authority_id": "sql", "kind": "sql", "incarnation": strings.Repeat("4", 32), "position": "1", "read_nonce": sqlNonce, "consistency": "ack_before_read_start", "started_at": "2026-01-01T00:00:00Z", "completed_at": "2026-01-01T00:00:00Z"},
			},
			"results": []any{
				map[string]any{"reference": ref, "status": "resolved", "header": header, "profile": profile, "authority_refs": []any{imNonce, sqlNonce}, "predicates": predicates},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer srv.Close()

	conf := srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	conf.InsecureSkipVerify = false
	key := Key{Origin: srv.URL, BotUID: "bot", SpaceID: "space"}
	self, err := NewSelfPolicyReader(key.Origin, key.BotUID, "bf_synthetic", conf, key.Origin)
	if err != nil {
		t.Fatal(err)
	}
	pins := map[string]AuthorityPin{
		"im":  {Kind: "im", Incarnation: strings.Repeat("3", 32)},
		"sql": {Kind: "sql", Incarnation: strings.Repeat("4", 32)},
	}
	reader, err := NewNativeReader(self, pins)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.ConfigureBounds(0, 2, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted invalid bounds", err)
	}
	// Each Resolve adds 3 read fences (1 read_id + 2 authority nonces).
	// Allow 9 read fences (3 Resolves) and 2 distinct header observations, 1 active generation.
	if err := reader.ConfigureBounds(9, 2, 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	obs1, err := reader.Resolve(ctx, key, "admit", []string{"deliv_1"})
	if err != nil || len(obs1) != 1 {
		t.Fatal(err)
	}
	if err := reader.ConfigureBounds(9, 2, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("reconfigured bounds after reads", err)
	}

	// Re-resolving the same header_reference with identical HeaderV1 bytes succeeds
	// and does not consume an additional observation slot.
	if _, err := reader.Resolve(ctx, key, "contributor", []string{"deliv_1"}); err != nil {
		t.Fatal(err)
	}
	if len(reader.observations) != 1 || len(reader.reads) != 6 {
		t.Fatalf("unexpected state: observations=%d reads=%d", len(reader.observations), len(reader.reads))
	}

	// Mutating HeaderV1 bytes for an existing header_reference fails closed without mutating fences.
	msgID = "999"
	if _, err := reader.Resolve(ctx, key, "contributor", []string{"deliv_1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("accepted mutated HeaderV1 for existing header_reference", err)
	}
	msgID = "100"
	if len(reader.reads) != 6 {
		t.Fatal("failedResolve mutated read fences", len(reader.reads))
	}

	// Second distinct header_reference reaches observation limit (2) and read limit (9).
	headerRef = "header_2"
	msgID = "101"
	seq = "101"
	obs2, err := reader.Resolve(ctx, key, "contributor", []string{"deliv_2"})
	if err != nil || len(obs2) != 1 {
		t.Fatal(err)
	}
	if len(reader.observations) != 2 || len(reader.reads) != 9 {
		t.Fatalf("unexpected state at ceiling: observations=%d reads=%d", len(reader.observations), len(reader.reads))
	}

	// Saturation on reads fails closed without evicting earlier observations or reads.
	if _, err := reader.Resolve(ctx, key, "contributor", []string{"deliv_2"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("read ceiling did not fail closed", err)
	}
	// Raise maxReads via a second reader to isolate observation-ceiling saturation.
	readerObs, err := NewNativeReader(self, pins)
	if err != nil {
		t.Fatal(err)
	}
	if err := readerObs.ConfigureBounds(100, 1, 1); err != nil {
		t.Fatal(err)
	}
	headerRef = "header_1"
	msgID = "100"
	seq = "100"
	firstObs, err := readerObs.Resolve(ctx, key, "admit", []string{"deliv_1"})
	if err != nil {
		t.Fatal(err)
	}
	headerRef = "header_2"
	msgID = "101"
	seq = "101"
	if _, err := readerObs.Resolve(ctx, key, "contributor", []string{"deliv_2"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("observation ceiling did not fail closed", err)
	}
	rawHeader, err := PrivateJSON(firstObs[0].Header())
	if err != nil {
		t.Fatal(err)
	}
	decodedHeader, err := decodeStrict(strings.NewReader(string(rawHeader)), 8192)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readerObs.ObservedHeader(key, decodedHeader); err != nil {
		t.Fatal("existing observation evicted at ceiling", err)
	}

	// Generation ceiling fails closed at 1 active generation until ForgetGeneration reclaims it.
	gen1 := NewID()
	gen2 := NewID()
	if err := reader.BindGeneration(gen1, obs1[0], []Observation{obs2[0]}); err != nil {
		t.Fatal(err)
	}
	if err := reader.BindGeneration(gen2, obs1[0], nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("generation ceiling did not fail closed", err)
	}
	reader.ForgetGeneration(gen1)
	if err := reader.BindGeneration(gen2, obs1[0], nil); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinatorWatermarkSeedsNeedsAndPreservesFreshRevocation(t *testing.T) {
	p := fixture(t)
	p.Invocation = Stage{"E", []Rule{rule("admit", "allow", "alice", "group", "", false)}}
	p.Capability = Stage{"U", []Rule{}}
	raw := encode(t, p)
	var trace []string
	var lastNeeds Needs
	c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) {
		trace = append(trace, "B")
		return raw, nil
	}), factFunc(func(ctx context.Context, b Binding, n Needs) (Facts, error) {
		trace = append(trace, "F")
		lastNeeds = n
		return allFacts(ctx, b, n)
	}))
	// First boundary on an unseen scope starts at Needs{false,false} and activates Invocation=="E" via FBFB.
	if err := c.atBoundary(context.Background(), i, testCap, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(trace, "") != "FBFB" || !lastNeeds.Invocation || lastNeeds.Capability {
		t.Fatalf("first boundary trace=%v lastNeeds=%+v", trace, lastNeeds)
	}

	// Second boundary on the same scope seeds Needs{true,false} from watermark and completes in one fresh FB round.
	trace = nil
	if err := c.atBoundary(context.Background(), i, testCap, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(trace, "") != "FB" || !lastNeeds.Invocation || lastNeeds.Capability {
		t.Fatalf("seeded boundary trace=%v lastNeeds=%+v", trace, lastNeeds)
	}

	// Transitioning Capability from U -> E on revision 1 triggers FBFB re-read and enforces denial.
	trace = nil
	p.Revision = "1"
	p.Capability = Stage{"E", []Rule{}}
	raw = encode(t, p)
	err := c.atBoundary(context.Background(), i, testCap, func() error { t.Fatal("denied dispatch"); return nil })
	category(t, err, "capability_denied")
	if strings.Join(trace, "") != "FBFB" || !lastNeeds.Invocation || !lastNeeds.Capability {
		t.Fatalf("U->E transition trace=%v lastNeeds=%+v", trace, lastNeeds)
	}
}
