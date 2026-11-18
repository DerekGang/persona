package wire

import "testing"

func TestIsEmoji(t *testing.T) {
	for _, s := range []string{"💡", "❄️", "❄", "📺", "🔌", "🌀", "👍🏽", "👩‍💻", "👨‍👩‍👧‍👦", "🏳️‍🌈", "🇨🇳", "1️⃣", "#⃣",
		"🏴\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", "©️", "♨️", "🫖"} {
		if !IsEmoji(s) {
			t.Errorf("IsEmoji(%q) = false", s)
		}
	}
	for _, s := range []string{"", "a", "灯", "💡💡", "💡 ", " 💡", "💡a", "‍", "💡‍", "‍💡", "🏽", "️",
		"🇨", "🇨🇳🇯", "1", "12", "1️", "🏴\U000E0067", "<b>💡</b>"} {
		if IsEmoji(s) {
			t.Errorf("IsEmoji(%q) = true", s)
		}
	}
}
