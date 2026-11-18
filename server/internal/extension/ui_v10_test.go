package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

func v10Example(t *testing.T) *View {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "extension", "examples", "View.v10-home.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseView(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// An app of UI version 10 gets the example as the extension drew it; an app of 6 to 9 gets every tile without its
// emoji and otherwise byte for byte (its icon is what it draws); an app of 5 gets rows, none saying an emoji.
func TestFitV10Emoji(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v10Example(t)
	want, _ := json.Marshal(v)
	if dropped, err := Fit(v, page, 10); err != nil || len(dropped) > 0 {
		t.Fatalf("for 10: dropped %v, %v", dropped, err)
	}
	if got, _ := json.Marshal(v); string(got) != string(want) {
		t.Errorf("an app of 10 gets it changed\nwant %s\n got %s", want, got)
	}
	drawn := tilesOf(v10Example(t))
	for version := 6; version < 10; version++ {
		v := v10Example(t)
		if _, err := Fit(v, page, version); err != nil {
			t.Fatalf("for %d: %v", version, err)
		}
		got := tilesOf(v)
		if len(got) != len(drawn) {
			t.Fatalf("for %d: %d tiles, want %d", version, len(got), len(drawn))
		}
		for i, tl := range got {
			if tl.Emoji != "" || tl.Icon == "" {
				t.Errorf("for %d: tile %q keeps its emoji or has no icon: %+v", version, tl.Title, tl)
			}
			w := drawn[i]
			w.Emoji = ""
			a, _ := json.Marshal(w)
			b, _ := json.Marshal(tl)
			if string(a) != string(b) {
				t.Errorf("for %d: tile %q changed beyond its emoji\nwant %s\n got %s", version, tl.Title, a, b)
			}
		}
	}
	old := v10Example(t)
	if _, err := Fit(old, page, 5); err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(old); strings.ContainsAny(string(raw), "💡❄📺🌀🔊") {
		t.Errorf("an app of 5 is told an emoji: %s", raw)
	}
}

// CheckView takes one emoji beside an icon, and refuses text, two emoji, and an emoji with a picture or a ring.
func TestCheckViewEmoji(t *testing.T) {
	grid := func(tl Tile) *View {
		return &View{Sections: []Section{{Items: []UIElem{{Type: "grid", Tiles: []Tile{tl}}}}}}
	}
	ratio := 0.5
	for name, tl := range map[string]Tile{
		"text":       {Title: "灯", Emoji: "灯"},
		"two":        {Title: "灯", Emoji: "💡💡"},
		"padded":     {Title: "灯", Emoji: " 💡"},
		"with image": {Title: "门口", Emoji: "📷", Image: &ImageSource{URL: "https://example.com/a.jpg"}},
		"with gauge": {Title: "5 hours", Emoji: "⏱️", Gauge: &Gauge{Ratio: &ratio}},
	} {
		if err := CheckView(grid(tl)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, tl := range map[string]Tile{
		"with icon":   {Title: "灯", Emoji: "💡", Icon: "light"},
		"alone":       {Title: "空调", Emoji: "❄️"},
		"zwj":         {Title: "书房", Emoji: "👩‍💻"},
		"with switch": {Key: "k", Title: "灯", Emoji: "💡", Control: &RowControl{Kind: "switch", Name: "p", Action: wire.Action{ID: "ctl"}}},
	} {
		if err := CheckView(grid(tl)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
