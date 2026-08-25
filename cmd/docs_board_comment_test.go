package cmd

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
)

func closeBoardNumber(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func TestParseBoardPointStrict(t *testing.T) {
	for _, value := range []string{"1x,2", "1,2x", "1e2x,2", "NaN,2", "1,Inf"} {
		if _, _, err := parseBoardPoint(value); err == nil {
			t.Errorf("parseBoardPoint(%q) accepted invalid input", value)
		}
	}
	x, y, err := parseBoardPoint(" 1e2 , -2.5 ")
	if err != nil || x != 100 || y != -2.5 {
		t.Fatalf("valid point = %v,%v err=%v", x, y, err)
	}
}

func TestBoardAnchorCoordinateRangeIsConsistent(t *testing.T) {
	if _, _, err := parseBoardPoint("1000001,0"); err == nil || !strings.Contains(err.Error(), "within") {
		t.Fatalf("point range error=%v", err)
	}
	elements := []map[string]any{{"id": "r", "type": "rectangle", "x": float64(999999), "y": float64(0), "width": float64(10), "height": float64(10)}}
	if _, _, err := boardElementAnchor(elements, []string{"r"}); err == nil || !strings.Contains(err.Error(), "computed anchor") {
		t.Fatalf("computed anchor range error=%v", err)
	}
}

func TestBoardLinearSeedMustBeInteger(t *testing.T) {
	element := map[string]any{"type": "line", "seed": 1.5}
	if _, err := boardLinearRoughOptions(element, []boardPoint{{0, 0}, {20, 20}}); err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("seed error=%v", err)
	}
}

func TestBoardElementAnchorRotatedRectangle(t *testing.T) {
	elements := []map[string]any{{"id": "r", "type": "rectangle", "x": float64(10), "y": float64(20), "width": float64(40), "height": float64(20), "angle": math.Pi / 2}}
	anchor, _, err := boardElementAnchor(elements, []string{"r"})
	if err != nil {
		t.Fatal(err)
	}
	if !closeBoardNumber(anchor["x"].(float64), 40) || !closeBoardNumber(anchor["y"].(float64), 10) {
		t.Fatalf("anchor=%v, want 40,10", anchor)
	}
}

func TestBoardElementAnchorFreedrawUsesPointsAndRotation(t *testing.T) {
	elements := []map[string]any{{
		"id": "f", "type": "freedraw", "x": float64(100), "y": float64(200),
		"width": float64(999), "height": float64(999), "angle": math.Pi / 2,
		"points": []any{[]any{float64(0), float64(0)}, []any{float64(20), float64(10)}, []any{float64(5), float64(30)}},
	}}
	anchor, _, err := boardElementAnchor(elements, []string{"f"})
	if err != nil {
		t.Fatal(err)
	}
	if !closeBoardNumber(anchor["x"].(float64), 125) || !closeBoardNumber(anchor["y"].(float64), 205) {
		t.Fatalf("anchor=%v, want 125,205", anchor)
	}
}

func TestBoardElementAnchorRotatedArrowMatchesWebRoughBounds(t *testing.T) {
	elements := []map[string]any{{
		"id": "a", "type": "arrow", "x": float64(100), "y": float64(100), "width": float64(40), "height": float64(20),
		"angle": math.Pi / 2, "points": []any{[]any{float64(.5), float64(.5)}, []any{float64(39.5), float64(19.5)}},
		"seed": float64(2072220693), "roughness": float64(1), "strokeStyle": "solid",
	}}
	anchor, _, err := boardElementAnchor(elements, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if !closeBoardNumber(anchor["x"].(float64), 129.5) || !closeBoardNumber(anchor["y"].(float64), 90.5) {
		t.Fatalf("anchor=%v, want 129.5,90.5", anchor)
	}
}

func TestBoardElementAnchorMixedSelectionUsesVisualUnion(t *testing.T) {
	elements := []map[string]any{
		{"id": "r", "type": "rectangle", "x": float64(10), "y": float64(20), "width": float64(40), "height": float64(20), "angle": math.Pi / 2},
		{"id": "f", "type": "freedraw", "x": float64(100), "y": float64(200), "angle": math.Pi / 2, "points": []any{[]any{float64(0), float64(0)}, []any{float64(20), float64(10)}, []any{float64(5), float64(30)}}},
	}
	anchor, _, err := boardElementAnchor(elements, []string{"r", "f"})
	if err != nil {
		t.Fatal(err)
	}
	if !closeBoardNumber(anchor["x"].(float64), 72.5) || !closeBoardNumber(anchor["y"].(float64), 117.5) {
		t.Fatalf("anchor=%v, want 72.5,117.5", anchor)
	}
}

func TestDocsBoardCommentDryRunPostsCanonicalAnchor(t *testing.T) {
	body := `{"elements":[{"id":"r","type":"rectangle","x":10,"y":20,"width":40,"height":20,"angle":1.5707963267948966,"isDeleted":false}],"files":{},"baseVersion":"BV"}`
	_, capture := sceneTestFactory(t, serveSceneTest(body))
	stdout, _, err := execRoot(t, capture.f, "--dry-run", "docs", "comments", "add", "d1", "--body", "review", "--element-id", "r")
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	if data["method"] != http.MethodPost || data["url"] != "/v1/bot/docs/d1/comments" {
		t.Fatalf("request=%v", data)
	}
	requestBody := data["body"].(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(requestBody["anchorStart"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var anchor map[string]any
	if err := json.Unmarshal(decoded, &anchor); err != nil {
		t.Fatal(err)
	}
	if !closeBoardNumber(anchor["x"].(float64), 40) || !closeBoardNumber(anchor["y"].(float64), 10) || requestBody["anchorStart"] != requestBody["anchorEnd"] {
		t.Fatalf("anchor/body=%v", requestBody)
	}
}

func TestDocsCommentsAddOwnsBoardFlagsWithoutAlias(t *testing.T) {
	_, capture := sceneTestFactory(t, serveSceneTest(`{"elements":[],"files":{},"baseVersion":"BV"}`))
	root := NewRootCmd(capture.f.Factory)
	if commandAt(root, "docs", "comments", "add-board") != nil {
		t.Fatal("docs comments add-board must not be registered")
	}
	add := commandAt(root, "docs", "comments", "add")
	if add == nil || add.Flags().Lookup("point") == nil || add.Flags().Lookup("element-id") == nil {
		t.Fatal("docs comments add is missing board-friendly flags")
	}
}

func TestDocsCommentsAddBoardFlagValidation(t *testing.T) {
	_, capture := sceneTestFactory(t, serveSceneTest(`{"elements":[],"files":{},"baseVersion":"BV"}`))
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"data conflict", []string{"docs", "comments", "add", "d1", "--data", `{"body":"x"}`, "--point", "1,2"}, "cannot be combined"},
		{"reply conflict", []string{"docs", "comments", "add", "d1", "--body", "x", "--point", "1,2", "--parentId", "7"}, "--parentId"},
		{"anchor text conflict", []string{"docs", "comments", "add", "d1", "--body", "x", "--point", "1,2", "--anchorText", "selection"}, "--anchorText"},
		{"anchor start conflict", []string{"docs", "comments", "add", "d1", "--body", "x", "--point", "1,2", "--anchorStart", "opaque"}, "--anchorStart"},
		{"anchor end conflict", []string{"docs", "comments", "add", "d1", "--body", "x", "--point", "1,2", "--anchorEnd", "opaque"}, "--anchorEnd"},
		{"block path conflict", []string{"docs", "comments", "add", "d1", "--body", "x", "--point", "1,2", "--blockPath", "0,1"}, "--blockPath"},
		{"occurrence conflict", []string{"docs", "comments", "add", "d1", "--body", "x", "--point", "1,2", "--occurrence", "2"}, "--occurrence"},
		{"anchor conflict", []string{"docs", "comments", "add", "d1", "--body", "x", "--point", "1,2", "--element-id", "e"}, "exactly one"},
		{"missing body", []string{"docs", "comments", "add", "d1", "--point", "1,2"}, "--body must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture.f.Out.Reset()
			capture.f.ErrOut.Reset()
			_, _, err := execRoot(t, capture.f, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestDocsCommentsAddBodyOnlyKeepsGeneratedCommandBehavior(t *testing.T) {
	var posted map[string]any
	_, capture := sceneTestFactory(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	if _, _, err := execRoot(t, capture.f, "docs", "comments", "add", "d1", "--body", "reply", "--parentId", "7"); err != nil {
		t.Fatal(err)
	}
	if posted["body"] != "reply" || posted["parentId"] != float64(7) {
		t.Fatalf("posted=%#v", posted)
	}
}

func TestDocsCommentsAddBoardRejectsOpaqueDocIDSentinels(t *testing.T) {
	for _, docID := range []string{"", ".", ".."} {
		t.Run(docID, func(t *testing.T) {
			_, capture := sceneTestFactory(t, serveSceneTest(`{"elements":[],"files":{},"baseVersion":"BV"}`))
			_, _, err := execRoot(t, capture.f, "docs", "comments", "add", docID, "--body", "x", "--point", "1,2")
			if err == nil {
				t.Fatalf("docId %q was accepted", docID)
			}
			if capture.requests != 0 {
				t.Fatalf("docId %q made %d requests", docID, capture.requests)
			}
		})
	}
}

func TestDocsCommentsAddElementIDPreservesComma(t *testing.T) {
	scene := `{"elements":[{"id":"box,1","type":"rectangle","x":0,"y":0,"width":10,"height":10}],"files":{},"baseVersion":"BV"}`
	_, capture := sceneTestFactory(t, serveSceneTest(scene))
	if _, _, err := execRoot(t, capture.f, "--dry-run", "docs", "comments", "add", "d1", "--body", "x", "--element-id", "box,1"); err != nil {
		t.Fatal(err)
	}
}

func TestDocsBoardCommentsPostThenListRoundTripAllAnchorKinds(t *testing.T) {
	scene := `{"elements":[{"id":"r","type":"rectangle","x":10,"y":20,"width":40,"height":20,"angle":1.5707963267948966,"isDeleted":false},{"id":"a","type":"rectangle","x":100,"y":200,"width":20,"height":10,"angle":0,"isDeleted":false}],"files":{},"baseVersion":"BV"}`
	var stored []map[string]any
	_, capture := sceneTestFactory(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/bot/docs/d1/scene":
			_, _ = w.Write([]byte(scene))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/bot/docs/d1/comments":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode POST: %v", err)
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			body["id"] = float64(len(stored) + 1)
			stored = append(stored, body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/bot/docs/d1/comments":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": stored, "nextCursor": nil})
		default:
			http.NotFound(w, r)
		}
	})

	commands := [][]string{
		{"docs", "comments", "add", "d1", "--body", "point", "--point", "12.5,-4"},
		{"docs", "comments", "add", "d1", "--body", "single", "--element-id", "r"},
		{"docs", "comments", "add", "d1", "--body", "multi", "--element-id", "r", "--element-id", "a"},
	}
	for _, args := range commands {
		capture.f.Out.Reset()
		capture.f.ErrOut.Reset()
		if _, _, err := execRoot(t, capture.f, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	capture.f.Out.Reset()
	capture.f.ErrOut.Reset()
	stdout, _, err := execRoot(t, capture.f, "docs", "comments", "list", "d1", "--includeResolved", "1")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data struct {
			Items []struct {
				Body        string `json:"body"`
				AnchorStart string `json:"anchorStart"`
				AnchorEnd   string `json:"anchorEnd"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 3 {
		t.Fatalf("listed %d comments, want 3: %s", len(envelope.Data.Items), stdout)
	}
	anchors := map[string]map[string]any{}
	for _, item := range envelope.Data.Items {
		if item.AnchorStart != item.AnchorEnd {
			t.Fatalf("%s anchor pair differs", item.Body)
		}
		raw, err := base64.StdEncoding.DecodeString(item.AnchorStart)
		if err != nil {
			t.Fatal(err)
		}
		var anchor map[string]any
		if err := json.Unmarshal(raw, &anchor); err != nil {
			t.Fatal(err)
		}
		anchors[item.Body] = anchor
	}
	point := anchors["point"]
	if point["kind"] != "point" || point["version"] != float64(1) || point["x"] != 12.5 || point["y"] != -4.0 {
		t.Fatalf("point anchor=%v", point)
	}
	single := anchors["single"]
	if single["kind"] != "element" || single["elementId"] != "r" || single["x"] != 40.0 || single["y"] != 10.0 {
		t.Fatalf("single anchor=%v", single)
	}
	multi := anchors["multi"]
	ids, _ := multi["elementIds"].([]any)
	if multi["kind"] != "element" || multi["elementId"] != "r" || len(ids) != 2 || ids[0] != "r" || ids[1] != "a" {
		t.Fatalf("multi anchor=%v", multi)
	}
}
