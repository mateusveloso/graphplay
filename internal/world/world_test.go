package world

import (
	"slices"
	"strings"
	"testing"
)

func cellar(t *testing.T) World {
	t.Helper()
	w, err := Load("cellar")
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func play(t *testing.T, g *Game, cmds ...string) Observation {
	t.Helper()
	var obs Observation
	for _, s := range cmds {
		c, err := Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		obs = g.Do(c)
	}
	return obs
}

func TestEmbeddedWorldsValidate(t *testing.T) {
	t.Parallel()
	ids := Available()
	if !slices.Contains(ids, "cellar") {
		t.Fatalf("cellar missing from %v", ids)
	}
	for _, id := range ids {
		if _, err := Load(id); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestTheIntendedSolutionWins(t *testing.T) {
	t.Parallel()
	g := New(cellar(t))
	obs := play(t, g, "take lantern", "go north", "go north", "go north", "go east")
	if obs.Riddle == "" || obs.Won {
		t.Fatalf("the carved door should pose its riddle first: %+v", obs)
	}
	obs = play(t, g, "answer Echo", "go east")
	if !obs.Won || obs.Room != "treasury" {
		t.Fatalf("want victory in the treasury, got %+v", obs)
	}
}

func TestDarkPassageKillsWithoutTheLantern(t *testing.T) {
	t.Parallel()
	obs := play(t, New(cellar(t)), "go north", "go north")
	if !obs.Dead || obs.Room != "corridor" {
		t.Fatalf("want death on entering the dark, got %+v", obs)
	}
}

func TestTrollTakesTheCoinOrThePlayer(t *testing.T) {
	t.Parallel()
	w := cellar(t)
	if obs := play(t, New(w), "go north", "go west"); !obs.Dead {
		t.Fatalf("no coin: want death, got %+v", obs)
	}
	obs := play(t, New(w), "go east", "take coin", "go west", "go north", "go west")
	if obs.Dead || obs.Room != "troll_bridge" || slices.Contains(obs.Inventory, "coin") {
		t.Fatalf("with coin: want passage and the coin spent, got %+v", obs)
	}
}

func TestAPaidTollStaysPaid(t *testing.T) {
	t.Parallel()
	g := New(cellar(t))
	obs := play(t, g, "go east", "take coin", "go west", "go north", "go west", "go east", "go west")
	if obs.Dead || obs.Room != "troll_bridge" {
		t.Fatalf("crossing back over a paid bridge must be free: %+v", obs)
	}
}

func TestWrongAnswerKeepsTheDoorShut(t *testing.T) {
	t.Parallel()
	g := New(cellar(t))
	obs := play(t, g, "take lantern", "go north", "go north", "go north", "go east", "answer wind", "go east")
	if obs.Room != "riddle_room" || obs.Won {
		t.Fatalf("door should stay shut, got %+v", obs)
	}
}

func TestObservationNeverLeaksTheMap(t *testing.T) {
	t.Parallel()
	obs := New(cellar(t)).Observe()
	want := []string{"east", "north"}
	if !slices.Equal(obs.Exits, want) || len(obs.Items) != 1 {
		t.Fatalf("want sorted exits and one visible item, got %+v", obs)
	}
}

func TestReplayReproducesState(t *testing.T) {
	t.Parallel()
	w := cellar(t)
	g := New(w)
	play(t, g, "take lantern", "go north", "go north")
	again := Replay(w, g.Log())
	if a, b := g.Observe(), again.Observe(); a.Room != b.Room || !slices.Equal(a.Inventory, b.Inventory) {
		t.Fatalf("replay diverged: %+v vs %+v", a, b)
	}
}

func TestCommandsAfterDeathAreInert(t *testing.T) {
	t.Parallel()
	g := New(cellar(t))
	obs := play(t, g, "go north", "go north", "go south", "take lantern")
	if !obs.Dead || obs.Room != "corridor" || len(obs.Inventory) != 0 {
		t.Fatalf("dead players do not move: %+v", obs)
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want Command
		ok   bool
	}{
		{"go north", Command{Verb: Go, Arg: "north"}, true},
		{"  TAKE  Lantern ", Command{Verb: Take, Arg: "lantern"}, true},
		{"look", Command{Verb: Look}, true},
		{"go", Command{}, false},
		{"dance", Command{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tt.in)
			if (err == nil) != tt.ok || got != tt.want {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestLookRevealsHiddenExitsOnce(t *testing.T) {
	t.Parallel()
	w, err := Load("caverns")
	if err != nil {
		t.Fatal(err)
	}
	g := New(w)
	obs := play(t, g, "go north", "go east")
	if slices.Contains(obs.Exits, "down") {
		t.Fatal("the hidden exit must not show before looking")
	}
	obs = play(t, g, "look")
	if !slices.Contains(obs.Exits, "down") || !strings.Contains(obs.Message, "steps lead down") {
		t.Fatalf("look should reveal the way down: %+v", obs)
	}
	if obs = play(t, g, "look"); !strings.Contains(obs.Message, "nothing you had not seen") {
		t.Fatalf("second look: %+v", obs)
	}
	if obs = play(t, g, "go down"); obs.Room != "crypt" {
		t.Fatalf("want the crypt, got %+v", obs)
	}
}

func TestCavernsIntendedSolution(t *testing.T) {
	t.Parallel()
	w, err := Load("caverns")
	if err != nil {
		t.Fatal(err)
	}
	obs := play(t, New(w),
		"go north", "go west", "take lantern", "go east", // lantern
		"go east", "look", "go down", "go north", "answer footsteps", "go north", "take coin", // coin
		"go south", "go up", "go west", "go north", "go north", "go north", // dark, troll, landing
		"go east", "go east", "answer map", "go east", // the cup, the antechamber, the riddle
	)
	if !obs.Won {
		t.Fatalf("want victory, got %+v", obs)
	}
}

func TestTrapKillsRegardlessOfInventory(t *testing.T) {
	t.Parallel()
	w, err := Load("caverns")
	if err != nil {
		t.Fatal(err)
	}
	obs := play(t, New(w),
		"go north", "go west", "take lantern", "go east",
		"go east", "look", "go down", "go north", "answer footsteps", "go north", "take coin",
		"go south", "go up", "go west", "go north", "go north", "go north", "go west")
	if !obs.Dead || !strings.Contains(obs.Message, "skull keeps you") {
		t.Fatalf("the skull arch must kill an equipped player too: %+v", obs)
	}
}
