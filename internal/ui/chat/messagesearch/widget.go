package messagesearch

import (
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/box"
	"github.com/ayn2op/tview/column"
	"github.com/ayn2op/tview/keybind"
	"github.com/ayn2op/tview/layout"
	"github.com/ayn2op/tview/list"
	"github.com/ayn2op/tview/row"
	"github.com/ayn2op/tview/text"
	"github.com/ayn2op/tview/textinput"
	"github.com/ayn2op/tview/viewport"
	"github.com/gdamore/tcell/v3"
)

const (
	prompt = "> "
	// queryHeight is the query and the line under it.
	queryHeight = 2
)

// widget is the query above the results. Unlike a picker's, its query is sent to Discord on submit rather than filtering as it is typed.
type widget struct {
	query       string
	queryCursor int
	results     viewport.Widget
	keybinds    config.PickerKeybinds
	selection   func(tview.KeyMsg) list.Action
}

var _ tview.Widget = widget{}

func (widget) Size() (width, height layout.Length) {
	return layout.Fill, layout.Fill
}

func (widget) Layout(limits layout.Limits) layout.Size {
	return layout.Atomic(limits, layout.Fill, layout.Fill)
}

func (w widget) Draw(screen tview.Screen, area tview.Rectangle) {
	w.layout().Draw(screen, area)
}

// Handle turns the picker's select and cancel keys into submitMsg and CancelMsg, sends its selection keys to the results, and typing to the query.
func (w widget) Handle(msg tview.Msg, area tview.Rectangle) tview.Msg {
	if key, ok := msg.(tview.KeyMsg); ok {
		switch {
		case keybind.Matches(key, w.keybinds.Select.Keybind):
			return submitMsg{}
		case keybind.Matches(key, w.keybinds.Cancel.Keybind):
			return CancelMsg{}
		case w.selection(key) != list.ActionNone:
			area.Y, area.Height = area.Y+queryHeight, max(area.Height-queryHeight, 0)
			return w.results.Handle(msg, area)
		}
	}
	return w.layout().Handle(msg, area)
}

func (w widget) layout() tview.Widget {
	query := textinput.New(w.query, w.queryCursor).
		Focused(true).
		OnInput(func(query string, cursor int) tview.Msg { return queryMsg{query, cursor} }).
		OnSubmit(submitMsg{})
	// A line below the query separates it from the results.
	var line tview.BorderSet
	line.Bottom = "─"
	header := box.New(row.New(text.New(prompt), query)).Borders(tview.BordersBottom).BorderSet(line).BorderStyle(tcell.StyleDefault.Dim(true))
	return column.New(column.New(header).Height(layout.Fixed(queryHeight)), w.results)
}
