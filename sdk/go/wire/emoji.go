package wire

// A tile's emoji (UI version 10, view.schema.json Tile.emoji): one emoji and nothing else, so a view's pictures stay
// a fixed vocabulary rather than free text.

const (
	zwj           = 0x200D
	vs16          = 0xFE0F
	keycap        = 0x20E3
	tagCancel     = 0xE007F
	blackFlag     = 0x1F3F4
	regionalFirst = 0x1F1E6
	regionalLast  = 0x1F1FF
)

// pictographs are the ranges of Unicode's Extended_Pictographic that emoji are drawn from (a superset of the emoji a
// font has: what is not drawn shows as text, never as something else).
var pictographs = [][2]rune{
	{0x00A9, 0x00A9}, {0x00AE, 0x00AE}, {0x203C, 0x203C}, {0x2049, 0x2049}, {0x2122, 0x2122}, {0x2139, 0x2139},
	{0x2194, 0x2199}, {0x21A9, 0x21AA}, {0x231A, 0x231B}, {0x2328, 0x2328}, {0x23CF, 0x23CF}, {0x23E9, 0x23F3},
	{0x23F8, 0x23FA}, {0x24C2, 0x24C2}, {0x25AA, 0x25AB}, {0x25B6, 0x25B6}, {0x25C0, 0x25C0}, {0x25FB, 0x25FE},
	{0x2600, 0x27BF}, {0x2934, 0x2935}, {0x2B05, 0x2B07}, {0x2B1B, 0x2B1C}, {0x2B50, 0x2B50}, {0x2B55, 0x2B55},
	{0x3030, 0x3030}, {0x303D, 0x303D}, {0x3297, 0x3297}, {0x3299, 0x3299},
	{0x1F000, 0x1F1E5}, {0x1F200, 0x1F3FA}, {0x1F400, 0x1FAFF},
}

func pictograph(r rune) bool {
	for _, p := range pictographs {
		if r >= p[0] && r <= p[1] {
			return true
		}
	}
	return false
}

func skinTone(r rune) bool { return r >= 0x1F3FB && r <= 0x1F3FF }
func regional(r rune) bool { return r >= regionalFirst && r <= regionalLast }
func tag(r rune) bool      { return r >= 0xE0020 && r <= 0xE007E }

// IsEmoji says s is exactly one emoji: a pictograph with an optional skin tone and variation selector, several of
// them joined by zero-width joiners (👩‍💻), a flag of two regional indicators (🇨🇳) or of tags (🏴 with its tags), or a
// keycap (1️⃣). Text, two emoji side by side, and a joiner, modifier or selector on its own are not.
func IsEmoji(s string) bool {
	rs := []rune(s)
	if len(rs) == 0 {
		return false
	}
	switch {
	case regional(rs[0]):
		return len(rs) == 2 && regional(rs[1])
	case rs[0] == '#' || rs[0] == '*' || rs[0] >= '0' && rs[0] <= '9':
		return len(rs) == 3 && rs[1] == vs16 && rs[2] == keycap || len(rs) == 2 && rs[1] == keycap
	}
	i := 0
	for {
		// one element: a pictograph, its skin tone, its variation selector
		if i >= len(rs) || !pictograph(rs[i]) {
			return false
		}
		flag := rs[i] == blackFlag
		i++
		if i < len(rs) && skinTone(rs[i]) {
			i++
		}
		if i < len(rs) && rs[i] == vs16 {
			i++
		}
		if flag && i < len(rs) && tag(rs[i]) { // a subdivision's flag: tags, then their end
			for i < len(rs) && tag(rs[i]) {
				i++
			}
			if i >= len(rs) || rs[i] != tagCancel {
				return false
			}
			i++
		}
		if i == len(rs) {
			return true
		}
		if rs[i] != zwj {
			return false
		}
		i++
	}
}
