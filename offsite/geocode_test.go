package offsite

import "testing"

func TestParseLinesAndGazetteer(t *testing.T) {
	places := ParseLines("Alice: Auckland\n3x Sydney\nBob - London, UK\nBerlin x2\nSFO\n37.77,-122.42\n# c\nNowhereville")
	if len(places) != 7 {
		t.Fatalf("got %d places: %+v", len(places), places)
	}
	if places[0].Name != "Alice" || places[0].Query != "Auckland" || places[1].Count != 3 || places[3].Count != 2 || places[2].Name != "Bob" {
		t.Fatalf("bad parse: %+v", places)
	}
	g := &Geocoder{}
	got := g.Resolve(nil, places)
	for i, want := range []bool{true, true, true, true, true, true, false} {
		if got[i].Resolved != want {
			t.Fatalf("place %d %+v resolved=%v want %v", i, got[i], got[i].Resolved, want)
		}
	}
	if got[2].Label == "" || got[2].Lat < 51 || got[2].Lat > 52 {
		t.Fatalf("London UK resolved wrong: %+v", got[2])
	}
}
