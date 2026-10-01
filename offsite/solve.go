package offsite

import (
	"errors"
	"math"
	"sort"
	"strings"
)

type Person struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Count   int     `json:"count,omitempty"`
	Airport string  `json:"airport,omitempty"`
}

type Constraints struct {
	MaxFare     float64  `json:"max_fare_usd,omitempty"`
	MaxStops    *int     `json:"max_stops,omitempty"`
	Objective   string   `json:"objective,omitempty"`
	HourlyValue float64  `json:"hourly_value_usd,omitempty"`
	MaxHours    float64  `json:"max_hours,omitempty"`
	Countries   []string `json:"countries,omitempty"`
	Include     []string `json:"include,omitempty"`
	Exclude     []string `json:"exclude,omitempty"`
	MinRoutes   int      `json:"min_routes,omitempty"`
	TopN        int      `json:"top_n,omitempty"`
}

type Leg struct {
	Person   string  `json:"person"`
	Count    int     `json:"count"`
	Origin   string  `json:"origin"`
	FareUSD  float64 `json:"fare_usd"`
	Stops    int     `json:"stops"`
	Hours    float64 `json:"hours_one_way"`
	Km       float64 `json:"km"`
	GroundKm float64 `json:"ground_km"`
}

type Option struct {
	Airport  Airport `json:"airport"`
	TotalUSD float64 `json:"total_usd"`
	MeanUSD  float64 `json:"mean_usd"`
	MaxUSD   float64 `json:"max_usd"`
	MaxHours float64 `json:"max_hours_one_way"`
	Score    float64 `json:"score"`
	Legs     []Leg   `json:"legs"`
}

type Result struct {
	Options    []Option       `json:"options"`
	Considered int            `json:"considered"`
	Dropped    map[string]int `json:"dropped,omitempty"`
	Note       string         `json:"note"`
}

// groundUSDPerKm prices the trip to an airport farther than the nearest one.
const groundUSDPerKm = 0.35

const Note = "Fares are round-trip economy USD per person (plus ground transport to a farther airport) from a distance model plus 2014 OpenFlights route data; use them to rank destinations, then confirm live prices."

var ErrNoPeople = errors.New("add at least one person")

func (db *DB) Solve(people []Person, c Constraints) (*Result, error) {
	if len(people) == 0 {
		return nil, ErrNoPeople
	}
	maxStops := 1
	if c.MaxStops != nil {
		maxStops = min(max(*c.MaxStops, 0), maxStopsCap)
	}
	minRoutes := c.MinRoutes
	if minRoutes == 0 {
		minRoutes = 40
	}
	topN := c.TopN
	if topN <= 0 || topN > 25 {
		topN = 5
	}
	exclude := set(c.Exclude)
	include := set(c.Include)
	countries := set(c.Countries)

	origins := make([][]Near, len(people))
	for i := range people {
		if people[i].Count < 1 {
			people[i].Count = 1
		}
		if ap := db.ByIATA[strings.ToUpper(people[i].Airport)]; ap != nil {
			origins[i] = []Near{{ap, Haversine(people[i].Lat, people[i].Lng, ap.Lat, ap.Lng)}}
		} else {
			origins[i] = db.Origins(people[i].Lat, people[i].Lng)
		}
	}

	res := &Result{Dropped: map[string]int{}, Note: Note}
	for di := range db.Airports {
		dest := &db.Airports[di]
		if len(include) > 0 {
			if !include[dest.IATA] {
				continue
			}
		} else if dest.Routes < minRoutes {
			continue
		}
		if exclude[dest.IATA] || (len(countries) > 0 && !countries[dest.Country]) {
			continue
		}
		res.Considered++
		opt := Option{Airport: *dest}
		feasible := true
		var n float64
		for pi, p := range people {
			best, ok := db.bestLeg(origins[pi], dest, maxStops, c)
			if !ok {
				res.Dropped["no flight within limits for "+p.Name]++
				feasible = false
				break
			}
			best.Person, best.Count = p.Name, p.Count
			opt.Legs = append(opt.Legs, best)
			cnt := float64(p.Count)
			opt.TotalUSD += best.FareUSD * cnt
			n += cnt
			opt.MaxUSD = math.Max(opt.MaxUSD, best.FareUSD)
			opt.MaxHours = math.Max(opt.MaxHours, best.Hours)
			opt.Score += best.Hours * 2 * cnt * c.HourlyValue
		}
		if !feasible {
			continue
		}
		opt.MeanUSD = roundTo(opt.TotalUSD/n, 5)
		switch c.Objective {
		case "fair":
			opt.Score += opt.MaxUSD*n + opt.TotalUSD*0.01
		case "balanced":
			opt.Score += opt.TotalUSD + n*opt.MaxUSD
		default:
			opt.Score += opt.TotalUSD
		}
		res.Options = append(res.Options, opt)
	}
	sort.Slice(res.Options, func(i, j int) bool { return res.Options[i].Score < res.Options[j].Score })
	res.Options = diversify(res.Options, topN)
	if len(res.Dropped) == 0 {
		res.Dropped = nil
	}
	return res, nil
}

func (db *DB) bestLeg(origins []Near, dest *Airport, maxStops int, c Constraints) (Leg, bool) {
	var best Leg
	found := false
	for _, o := range origins {
		stops, ok := db.Stops(o.Airport.IATA, dest.IATA)
		if !ok || stops > maxStops {
			continue
		}
		fare := roundTo(db.EstimateOneWay(o.Airport.IATA, dest.IATA, stops)*2+o.Km*groundUSDPerKm*2, 5)
		hours := db.EstimateHours(o.Airport.IATA, dest.IATA, stops)
		if o.Airport.IATA == dest.IATA {
			fare, hours = 0, 0
		}
		if (c.MaxFare > 0 && fare > c.MaxFare) || (c.MaxHours > 0 && hours > c.MaxHours) {
			continue
		}
		cand := Leg{Origin: o.Airport.IATA, FareUSD: fare, Stops: stops, Hours: math.Round(hours*10) / 10,
			Km: math.Round(db.Distance(o.Airport.IATA, dest.IATA)), GroundKm: math.Round(o.Km)}
		if !found || cand.FareUSD < best.FareUSD {
			best, found = cand, true
		}
	}
	return best, found
}

// diversify keeps the best option per metro area so the top N are not five
// airports serving one city.
func diversify(opts []Option, n int) []Option {
	var out []Option
	for _, o := range opts {
		dup := false
		for _, k := range out {
			if k.Airport.City == o.Airport.City && k.Airport.Country == o.Airport.Country {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, o)
		}
		if len(out) == n {
			break
		}
	}
	return out
}

func set(items []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range items {
		m[strings.ToUpper(strings.TrimSpace(s))] = true
	}
	return m
}
