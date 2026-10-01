package offsite

import "testing"

func TestSolveTransTasmanPicksAnAustralasianHub(t *testing.T) {
	db := Default()
	res, err := db.Solve([]Person{
		{Name: "Ana", Lat: -36.85, Lng: 174.76},
		{Name: "Ben", Lat: -33.87, Lng: 151.21},
		{Name: "Cy", Lat: -41.29, Lng: 174.78},
	}, Constraints{})
	if err != nil || len(res.Options) == 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if c := res.Options[0].Airport.Country; c != "AU" && c != "NZ" {
		t.Fatalf("best option %s %s is not in AU/NZ", res.Options[0].Airport.IATA, c)
	}
}

func TestMaxFareAndStopsFilter(t *testing.T) {
	db := Default()
	zero := 0
	res, _ := db.Solve([]Person{{Name: "A", Lat: -36.85, Lng: 174.76}, {Name: "B", Lat: 51.5, Lng: -0.12}}, Constraints{MaxStops: &zero, MaxFare: 400})
	if len(res.Options) != 0 {
		t.Fatalf("expected nothing for Auckland+London under 400 nonstop, got %+v", res.Options[0].Airport)
	}
	res, _ = db.Solve([]Person{{Name: "A", Lat: 40.64, Lng: -73.78}, {Name: "B", Lat: 51.5, Lng: -0.12}}, Constraints{MaxFare: 1200})
	if len(res.Options) == 0 {
		t.Fatal("NYC+London under 1200 should work")
	}
	for _, o := range res.Options {
		for _, l := range o.Legs {
			if l.FareUSD > 1200 || l.Stops > 1 {
				t.Fatalf("leg violates limits: %+v", l)
			}
		}
	}
}

func TestNoPeople(t *testing.T) {
	if _, err := Default().Solve(nil, Constraints{}); err != ErrNoPeople {
		t.Fatal(err)
	}
}
