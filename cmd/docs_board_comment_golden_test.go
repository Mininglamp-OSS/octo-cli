package cmd

import (
	"math"
	"testing"
)

func assertBoardBounds(t *testing.T, got, want boardBounds) {
	t.Helper()
	const tolerance = 1e-9
	values := []struct {
		name      string
		got, want float64
	}{{"minX", got.minX, want.minX}, {"minY", got.minY, want.minY}, {"maxX", got.maxX, want.maxX}, {"maxY", got.maxY, want.maxY}}
	for _, value := range values {
		if math.Abs(value.got-value.want) > tolerance {
			t.Errorf("%s=%0.15f, want %0.15f", value.name, value.got, value.want)
		}
	}
}

func indexBoardElements(elements []map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(elements))
	for _, element := range elements {
		out[element["id"].(string)] = element
	}
	return out
}

func TestBoardElementBoundsWebGoldens(t *testing.T) {
	// Golden provenance: evaluate the element JSON below with Excalidraw v0.18.1
	// getCommonBounds after rendering once (so ShapeCache is populated), supply
	// the complete elementsMap for bound text, and apply Octo's external-label
	// patch. Record the returned [minX,minY,maxX,maxY] doubles without rounding.
	cases := []struct {
		name    string
		element map[string]any
		want    boardBounds
	}{
		{
			name:    "positive slope rotated arrow",
			element: map[string]any{"id": "p", "type": "arrow", "x": float64(100), "y": float64(100), "width": float64(40), "height": float64(20), "angle": float64(.37), "seed": float64(12345), "roughness": float64(1), "strokeStyle": "solid", "points": []any{[]any{float64(.5), float64(.5)}, []any{float64(39.5), float64(19.5)}}},
			want:    boardBounds{105.25496336434946, 94.09138929342592, 134.74503663565054, 125.90861070657408},
		},
		{
			name:    "negative slope rotated arrow",
			element: map[string]any{"id": "n", "type": "arrow", "x": float64(100), "y": float64(100), "width": float64(40), "height": float64(20), "angle": float64(.37), "seed": float64(12345), "roughness": float64(1), "strokeStyle": "solid", "points": []any{[]any{float64(.5), float64(-.5)}, []any{float64(39.5), float64(.5)}}},
			want:    boardBounds{101.99591133308887, 92.48149081490961, 137.99506237975925, 107.51682000714915},
		},
		{
			name:    "rotated multi segment line",
			element: map[string]any{"id": "m", "type": "line", "x": float64(100), "y": float64(200), "width": float64(100), "height": float64(50), "angle": float64(.4), "seed": float64(12345), "roughness": float64(1), "strokeStyle": "solid", "points": []any{[]any{float64(0), float64(0)}, []any{float64(50), float64(50)}, []any{float64(100), float64(0)}}},
			want:    boardBounds{113.682408857572, 182.50255803449534, 205.7885082578605, 248.02652485007212},
		},
		{
			name:    "rotated rounded multi segment line",
			element: map[string]any{"id": "c", "type": "line", "x": float64(100), "y": float64(200), "width": float64(100), "height": float64(50), "angle": float64(.4), "seed": float64(12345), "roughness": float64(1), "strokeStyle": "solid", "roundness": map[string]any{"type": float64(2)}, "points": []any{[]any{float64(0), float64(0)}, []any{float64(50), float64(50)}, []any{float64(100), float64(0)}}},
			want:    boardBounds{113.52770677202165, 181.90070972177495, 204.95175985535482, 249.29341542782595},
		},
		{
			name:    "diamond",
			element: map[string]any{"id": "d", "type": "diamond", "x": float64(10), "y": float64(20), "width": float64(40), "height": float64(20), "angle": float64(.4)},
			want:    boardBounds{11.578780119942298, 20.789390059971147, 48.421219880057706, 39.21060994002885},
		},
		{
			name:    "rotated ellipse",
			element: map[string]any{"id": "e", "type": "ellipse", "x": float64(10), "y": float64(20), "width": float64(40), "height": float64(20), "angle": float64(.4)},
			want:    boardBounds{11.171670111184191, 17.937910894130937, 48.82832988881581, 42.06208910586906},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			elements := indexBoardElements([]map[string]any{tc.element})
			got, err := boardElementBounds(tc.element, elements)
			if err != nil {
				t.Fatal(err)
			}
			assertBoardBounds(t, got, tc.want)
		})
	}
}

func TestBoardElementBoundsRoundedBoundTextWebGolden(t *testing.T) {
	arrow := map[string]any{
		"id": "a", "type": "line", "x": float64(100), "y": float64(200), "width": float64(150), "height": float64(80), "angle": float64(.43),
		"seed": float64(12345), "roughness": float64(1), "strokeStyle": "solid", "roundness": map[string]any{"type": float64(2)},
		"points":        []any{[]any{float64(.5), float64(.5)}, []any{float64(50), float64(80)}, []any{float64(100), float64(20)}, []any{float64(149.5), float64(69.5)}},
		"boundElements": []any{map[string]any{"id": "t", "type": "text"}},
	}
	text := map[string]any{"id": "t", "type": "text", "x": float64(0), "y": float64(0), "width": float64(152), "height": float64(25), "angle": float64(0), "containerId": "a", "isDeleted": false}
	elements := indexBoardElements([]map[string]any{arrow, text})
	got, err := boardElementBounds(arrow, elements)
	if err != nil {
		t.Fatal(err)
	}
	assertBoardBounds(t, got, boardBounds{105.89533288856191, 172.1502302524064, 254.47989689987472, 338.63315852016547})
}

func TestRoundedShortLineKeepsFullRoughness(t *testing.T) {
	line := map[string]any{"type": "line", "roundness": map[string]any{"type": float64(2)}}
	points := []boardPoint{{0, 0}, {40, 30}}
	if got := adjustedBoardRoughness(line, points, 1); got != 1 {
		t.Fatalf("adjusted roughness=%v, want 1", got)
	}
}

func TestBoardElementBoundsSinglePointLinearDoesNotMergeBoundText(t *testing.T) {
	line := map[string]any{
		"id": "s", "type": "line", "x": float64(10), "y": float64(20), "width": float64(0), "height": float64(0), "angle": float64(0),
		"points": []any{[]any{float64(0), float64(0)}}, "boundElements": []any{map[string]any{"id": "st", "type": "text"}},
	}
	text := map[string]any{"id": "st", "type": "text", "x": float64(0), "y": float64(0), "width": float64(48), "height": float64(25), "containerId": "s", "isDeleted": false}
	elements := indexBoardElements([]map[string]any{line, text})
	got, err := boardElementBounds(line, elements)
	if err != nil {
		t.Fatal(err)
	}
	// Web getCommonBounds([line], elementsMap) returns the single point. Its
	// getBoundTextElementPosition side effect marks the orphan text deleted;
	// this pure CLI read computes the same bounds without mutating scene state.
	assertBoardBounds(t, got, boardBounds{10, 20, 10, 20})
}

func TestBoardElementBoundsBoundTextWebGolden(t *testing.T) {
	arrow := map[string]any{
		"id": "a", "type": "arrow", "x": float64(100), "y": float64(100), "width": float64(100), "height": float64(40), "angle": float64(.4),
		"seed": float64(12345), "roughness": float64(1), "strokeStyle": "solid", "points": []any{[]any{float64(.5), float64(.5)}, []any{float64(99.5), float64(39.5)}},
		"boundElements": []any{map[string]any{"id": "t", "type": "text"}},
	}
	text := map[string]any{"id": "t", "type": "text", "x": float64(0), "y": float64(0), "width": float64(128), "height": float64(25), "angle": float64(0), "containerId": "a", "isDeleted": false}
	elements := indexBoardElements([]map[string]any{arrow, text})
	got, err := boardElementBounds(arrow, elements)
	if err != nil {
		t.Fatal(err)
	}
	// Golden is getCommonBounds([arrow]) with the full scene map supplied for
	// bound-text lookup. The separate text element's own unrotated bounds are
	// intentionally not unioned a second time.
	assertBoardBounds(t, got, boardBounds{107.70745793993403, 28.92842539380763, 235.33872373001958, 157.23689732733447})
}
