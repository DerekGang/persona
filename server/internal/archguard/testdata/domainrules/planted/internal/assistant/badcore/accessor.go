package badcore

// Note is a note on a card. The zero Note is no note.
type Note struct {
	title Words
	body  Words
}

// Title hands the title out.
func (n Note) Title() Words { return n.title } // core-accessor
