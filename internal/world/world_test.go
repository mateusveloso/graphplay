package world

import (
	"slices"
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
