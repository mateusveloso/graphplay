// Package world is a small, deterministic text-adventure engine.
//
// It is the "operation" the graph plays against: it only ever reveals what a player
// would see (prose, exits, visible items), it never exposes the map, and every run is
// fully replayable from its command log. That last property is what makes death cheap:
// reload is a replay without the fatal command.
package world

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

//go:embed worlds/*.json
var worlds embed.FS

// Item is something a player can carry.
type Item struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Hazard kills a player who enters without the required item.
type Hazard struct {
	Requires string `json:"requires"`
	Consumes bool   `json:"consumes"`
	Pass     string `json:"pass"`
	Death    string `json:"death"`
}

// Lock bars an exit until its riddle is answered. Also lists accepted variants (a plural,
// a synonym); a riddle is not a spelling test.
type Lock struct {
	Riddle string   `json:"riddle"`
	Answer string   `json:"answer"`
	Also   []string `json:"also,omitempty"`
	Closed string   `json:"closed"`
	Opened string   `json:"opened"`
}

func (l Lock) accepts(text string) bool {
	text = strings.TrimSpace(text)
	if strings.EqualFold(text, l.Answer) {
		return true
	}
	return slices.ContainsFunc(l.Also, func(a string) bool { return strings.EqualFold(text, a) })
}

// Hidden is an exit that only appears after the player looks closely.
type Hidden struct {
	To     string `json:"to"`
	Reveal string `json:"reveal"`
}

// Room is one location. Dangers live in prose and in these fields; the player sees prose.
type Room struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Exits       map[string]string `json:"exits"`
	Hidden      map[string]Hidden `json:"hidden,omitempty"`
	Items       []string          `json:"items,omitempty"`
	Dark        bool              `json:"dark,omitempty"`
	Trap        bool              `json:"trap,omitempty"` // entering kills; nothing protects
	Death       string            `json:"death,omitempty"`
	Hazard      *Hazard           `json:"hazard,omitempty"`
	Locks       map[string]Lock   `json:"locks,omitempty"`
	Goal        bool              `json:"goal,omitempty"`
}

// exits returns the visible exits: the open ones plus any hidden one already revealed.
func (r *Room) exits(revealed bool) map[string]string {
	if !revealed || len(r.Hidden) == 0 {
		return r.Exits
	}
	all := maps.Clone(r.Exits)
	for dir, h := range r.Hidden {
		all[dir] = h.To
	}
	return all
}

// Strings are the engine's own sentences, so a world can speak its language. Missing ones
// fall back to English. Each has at most one %s.
type Strings struct {
	Taken           string `json:"taken,omitempty"`   // %s = item name
	NoItem          string `json:"no_item,omitempty"` // %s = item id
	CannotGo        string `json:"cannot_go,omitempty"`
	NothingHappens  string `json:"nothing_happens,omitempty"`
	NothingToAnswer string `json:"nothing_to_answer,omitempty"`
	NothingNew      string `json:"nothing_new,omitempty"`
	Cannot          string `json:"cannot,omitempty"` // %s = the command
}

var english = Strings{
	Taken:           "Taken: %s.",
	NoItem:          "There is no %s here.",
	CannotGo:        "You cannot go that way.",
	NothingHappens:  "Nothing happens.",
	NothingToAnswer: "There is nothing here to answer.",
	NothingNew:      "You see nothing you had not seen before.",
	Cannot:          "You cannot %q.",
}

func (s Strings) withDefaults() Strings {
	pick := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	return Strings{
		Taken:           pick(s.Taken, english.Taken),
		NoItem:          pick(s.NoItem, english.NoItem),
		CannotGo:        pick(s.CannotGo, english.CannotGo),
		NothingHappens:  pick(s.NothingHappens, english.NothingHappens),
		NothingToAnswer: pick(s.NothingToAnswer, english.NothingToAnswer),
		NothingNew:      pick(s.NothingNew, english.NothingNew),
		Cannot:          pick(s.Cannot, english.Cannot),
	}
}

// World is the static definition of a game.
type World struct {
	ID       string           `json:"id"`
	Lang     string           `json:"lang,omitempty"`  // BCP 47, informational: "en", "pt-BR"
	Light    string           `json:"light,omitempty"` // item id that makes dark rooms safe; "lantern" by default
	Start    string           `json:"start"`
	GoalText string           `json:"goal_text"`
	Items    map[string]Item  `json:"items"`
	Rooms    map[string]*Room `json:"rooms"`
	Strings  Strings          `json:"strings,omitempty"`
}

func (w World) light() string {
	if w.Light == "" {
		return "lantern"
	}
	return w.Light
}

// Load reads an embedded world by id.
func Load(id string) (World, error) {
	data, err := fs.ReadFile(worlds, "worlds/"+id+".json")
	if err != nil {
		return World{}, fmt.Errorf("world %q: %w", id, err)
	}
	var w World
	if err := json.Unmarshal(data, &w); err != nil {
		return World{}, fmt.Errorf("world %q: %w", id, err)
	}
	w.Strings = w.Strings.withDefaults()
	return w, w.validate()
}

// Available lists the embedded world ids.
func Available() []string {
	entries, _ := fs.ReadDir(worlds, "worlds")
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids
}

func (w World) validate() error {
	if _, ok := w.Rooms[w.Start]; !ok {
		return fmt.Errorf("start room %q does not exist", w.Start)
	}
	for id, r := range w.Rooms {
		for dir, to := range r.exits(true) {
			if _, ok := w.Rooms[to]; !ok {
				return fmt.Errorf("room %q exit %s points to unknown room %q", id, dir, to)
			}
		}
		for dir := range r.Locks {
			if _, ok := r.exits(true)[dir]; !ok {
				return fmt.Errorf("room %q locks an exit it does not have: %s", id, dir)
			}
		}
		for _, it := range r.Items {
			if _, ok := w.Items[it]; !ok {
				return fmt.Errorf("room %q holds unknown item %q", id, it)
			}
		}
	}
	return nil
}

// Command is one player action.
type Command struct {
	Verb string `json:"verb"`
	Arg  string `json:"arg,omitempty"`
}

// Verbs the engine understands.
const (
	Go     = "go"
	Take   = "take"
	Answer = "answer"
	Look   = "look"
)

// Parse reads "go north", "take lantern", "answer echo", "look".
func Parse(s string) (Command, error) {
	verb, arg, _ := strings.Cut(strings.TrimSpace(strings.ToLower(s)), " ")
	arg = strings.TrimSpace(arg)
	switch verb {
	case Go, Take, Answer:
		if arg == "" {
			return Command{}, fmt.Errorf("%q needs an argument", verb)
		}
		return Command{Verb: verb, Arg: arg}, nil
	case Look:
		return Command{Verb: Look}, nil
	}
	return Command{}, fmt.Errorf("unknown command %q", s)
}

func (c Command) String() string {
	if c.Arg == "" {
		return c.Verb
	}
	return c.Verb + " " + c.Arg
}

// Observation is everything a player can see after a command. Nothing else leaks.
type Observation struct {
	Room        string   `json:"room"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Exits       []string `json:"exits"`
	Items       []string `json:"items,omitempty"`
	Inventory   []string `json:"inventory,omitempty"`
	Message     string   `json:"message,omitempty"`
	Riddle      string   `json:"riddle,omitempty"`
	Dead        bool     `json:"dead,omitempty"`
	Won         bool     `json:"won,omitempty"`
}

// Game is a running instance of a World.
type Game struct {
	world     World
	room      string
	inventory []string
	items     map[string][]string // room -> items still there
	unlocked  map[string]bool     // "room/dir"
	revealed  map[string]bool     // rooms where "look" exposed the hidden exits
	paid      map[string]bool     // hazards already satisfied; a toll is paid once
	riddle    string              // riddle currently posed, if any
	message   string
	dead      bool
	won       bool
	log       []Command
}

// New starts a game at the world's start room.
func New(w World) *Game {
	w.Strings = w.Strings.withDefaults()
	g := &Game{
		world:    w,
		room:     w.Start,
		items:    make(map[string][]string, len(w.Rooms)),
		unlocked: make(map[string]bool),
		revealed: make(map[string]bool),
		paid:     make(map[string]bool),
	}
	for id, r := range w.Rooms {
		g.items[id] = slices.Clone(r.Items)
	}
	return g
}

// Replay rebuilds a game by applying commands to a fresh one.
func Replay(w World, commands []Command) *Game {
	g := New(w)
	for _, c := range commands {
		g.Do(c)
	}
	return g
}

// Log returns the commands applied so far.
func (g *Game) Log() []Command { return slices.Clone(g.log) }

// Over reports whether the game has ended, by death or victory.
func (g *Game) Over() bool { return g.dead || g.won }

// Observe describes the current situation.
func (g *Game) Observe() Observation {
	r := g.world.Rooms[g.room]
	obs := Observation{
		Room:        g.room,
		Name:        r.Name,
		Description: r.Description,
		Exits:       slices.Sorted(maps.Keys(r.exits(g.revealed[g.room]))),
		Items:       slices.Clone(g.items[g.room]),
		Inventory:   slices.Clone(g.inventory),
		Message:     g.message,
		Riddle:      g.riddle,
		Dead:        g.dead,
		Won:         g.won,
	}
	return obs
}

// Do applies one command and returns the resulting observation. Commands after the game
// is over are recorded but have no effect.
func (g *Game) Do(c Command) Observation {
	g.log = append(g.log, c)
	g.message = ""
	if g.Over() {
		return g.Observe()
	}
	switch c.Verb {
	case Look:
		g.look()
	case Take:
		g.take(c.Arg)
	case Answer:
		g.answer(c.Arg)
	case Go:
		g.move(c.Arg)
	default:
		g.message = fmt.Sprintf(g.world.Strings.Cannot, c.String())
	}
	return g.Observe()
}

func (g *Game) look() {
	r := g.world.Rooms[g.room]
	if len(r.Hidden) == 0 || g.revealed[g.room] {
		g.message = g.world.Strings.NothingNew
		return
	}
	g.revealed[g.room] = true
	for _, h := range r.Hidden {
		g.message = h.Reveal // one hidden exit per room is the designed case
	}
}

func (g *Game) take(item string) {
	here := g.items[g.room]
	i := slices.Index(here, item)
	if i < 0 {
		g.message = fmt.Sprintf(g.world.Strings.NoItem, item)
		return
	}
	g.items[g.room] = slices.Delete(here, i, i+1)
	g.inventory = append(g.inventory, item)
	g.message = fmt.Sprintf(g.world.Strings.Taken, g.world.Items[item].Name)
}

func (g *Game) answer(text string) {
	r := g.world.Rooms[g.room]
	for dir, lock := range r.Locks {
		key := g.room + "/" + dir
		if g.unlocked[key] {
			continue
		}
		if lock.accepts(text) {
			g.unlocked[key] = true
			g.riddle = ""
			g.message = lock.Opened
			return
		}
		g.message = g.world.Strings.NothingHappens
		return
	}
	g.message = g.world.Strings.NothingToAnswer
}

func (g *Game) move(dir string) {
	r := g.world.Rooms[g.room]
	to, ok := r.exits(g.revealed[g.room])[dir]
	if !ok {
		g.message = g.world.Strings.CannotGo
		return
	}
	if lock, locked := r.Locks[dir]; locked && !g.unlocked[g.room+"/"+dir] {
		g.riddle = lock.Riddle
		g.message = lock.Closed
		return
	}
	dest := g.world.Rooms[to]
	if dest.Trap {
		g.die(dest.Death)
		return
	}
	if dest.Dark && !slices.Contains(g.inventory, g.world.light()) {
		g.die(dest.Death)
		return
	}
	if h := dest.Hazard; h != nil && !g.paid[to] {
		i := slices.Index(g.inventory, h.Requires)
		if i < 0 {
			g.die(h.Death)
			return
		}
		if h.Consumes {
			g.inventory = slices.Delete(g.inventory, i, i+1)
		}
		g.paid[to] = true
		g.message = h.Pass
	}
	g.room, g.riddle = to, ""
	if dest.Goal {
		g.won = true
		g.message = g.world.GoalText
	}
}

func (g *Game) die(text string) {
	g.dead = true
	g.message = text
}
