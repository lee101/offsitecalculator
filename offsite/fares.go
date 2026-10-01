package offsite

import "math"

// FareModel is a deliberately simple, transparent price estimate: a distance
// curve with a few regional adjustments. It is a ranking aid, not a quote.
// Swap EstimateOneWay for a live fare API without touching the solver.
func (db *DB) EstimateOneWay(origin, dest string, stops int) float64 {
	a, b := db.ByIATA[origin], db.ByIATA[dest]
	if a == nil || b == nil || origin == dest {
		return 0
	}
	d := Haversine(a.Lat, a.Lng, b.Lat, b.Lng)
	fare := 55 + 0.09*math.Min(d, 1500) + 0.065*clamp(d-1500, 0, 3500) + 0.045*math.Max(d-5000, 0)
	if d < 400 {
		fare = math.Max(fare, 60)
	}
	switch {
	case a.Continent == "EU" && b.Continent == "EU" && d < 3000:
		fare *= 0.75
	case a.Country == b.Country && (a.Country == "US" || a.Country == "CA"):
		fare *= 0.95
	case isTasman(a.Country) && isTasman(b.Country):
		fare *= 1.15
	case isTasman(a.Country) || isTasman(b.Country):
		fare *= 1.08
	}
	switch stops {
	case 0:
		if a.Routes < 20 || b.Routes < 20 {
			fare *= 1.12
		}
	case 1:
		fare *= 0.9
	default:
		fare *= 0.85
	}
	return fare
}

func isTasman(c string) bool { return c == "AU" || c == "NZ" }

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// EstimateHours is one-way door-to-gate travel time.
func (db *DB) EstimateHours(origin, dest string, stops int) float64 {
	return db.Distance(origin, dest)/800 + 0.5 + float64(stops)*2.2
}

func roundTo(v, step float64) float64 { return math.Round(v/step) * step }
