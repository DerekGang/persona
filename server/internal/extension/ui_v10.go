package extension

// UI version 10 (docs/extensions/extensions.md "UI version 10", #10060): a tile's emoji, drawn large in its icon's
// place. downgrade10 leaves it out for an older app, which draws the tile's icon instead.

// downgrade10 takes every tile's emoji out, in place: the tile keeps its icon (an extension gives one beside the emoji
// for older apps), and its title and state say what it is.
func downgrade10(v *View) {
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			e := &v.Sections[i].Items[j]
			for k := range e.Tiles {
				e.Tiles[k].Emoji = ""
			}
		}
	}
}
