package offsite

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lee101/offsitecalculator/appnz"
)

const SystemPrompt = `You are an offsite planner. A team is spread across cities and wants a meeting spot that keeps flights cheap and fair.

Rules:
- Never invent fares, airports or routes. Get every number from tools.
- When asked for places to meet, call find_offsite_destinations with the team already on the map (omit people) and translate the user's wishes into constraints: a per-person fare cap, whether stops are fine, fairness vs lowest total, regions.
- Run it more than once when the user wants to compare ("what if we allow one stop?", "cap at $900"). Compare options by total cost, the most expensive traveller, and travel time.
- Fares are rough round-trip economy estimates from a distance model. Say so briefly and tell the user to confirm live prices before booking.
- Be concise: a short ranked list with one line of reasoning each, then one suggestion. No filler.`

type PeopleSource func() []Person
type ConstraintSource func() Constraints

func Tools(db *DB, geo *Geocoder, current PeopleSource, defaults ConstraintSource) []appnz.Tool {
	obj := func(s string) json.RawMessage { return json.RawMessage(s) }
	return []appnz.Tool{
		{
			Name:        "find_offsite_destinations",
			Description: "Rank airports/cities where the team could meet, minimising round-trip flight cost. Omit people to use the team on the map.",
			Parameters: obj(`{"type":"object","properties":{
"people":{"type":"array","description":"Optional override of the team","items":{"type":"object","properties":{"name":{"type":"string"},"location":{"type":"string","description":"City, address or IATA code"},"count":{"type":"integer"}},"required":["location"]}},
"max_fare_usd":{"type":"number","description":"Drop destinations where anyone's round-trip fare exceeds this"},
"max_stops":{"type":"integer","description":"0 nonstop only, 1 allows one connection, 2 allows two"},
"objective":{"type":"string","enum":["total","fair","balanced"],"description":"total = cheapest overall, fair = cheapest worst-off traveller, balanced = both"},
"hourly_value_usd":{"type":"number","description":"Value of an hour of travel, to penalise long itineraries"},
"max_hours":{"type":"number","description":"Max one-way travel hours for anyone"},
"countries":{"type":"array","items":{"type":"string"},"description":"Restrict to ISO country codes, e.g. [\"PT\",\"ES\"]"},
"include":{"type":"array","items":{"type":"string"},"description":"Only consider these IATA codes"},
"exclude":{"type":"array","items":{"type":"string"},"description":"Never consider these IATA codes"},
"top_n":{"type":"integer"}}}`),
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var args struct {
					People []struct {
						Name     string `json:"name"`
						Location string `json:"location"`
						Count    int    `json:"count"`
					} `json:"people"`
					Constraints
				}
				if err := json.Unmarshal(raw, &args); err != nil {
					return nil, err
				}
				c := defaults()
				merge(&c, args.Constraints)
				people := current()
				if len(args.People) > 0 {
					var places []Place
					for _, p := range args.People {
						places = append(places, Place{Query: p.Location, Name: firstNonEmpty(p.Name, p.Location), Count: max(p.Count, 1)})
					}
					people = nil
					for _, p := range geo.Resolve(ctx, places) {
						if !p.Resolved {
							return nil, fmt.Errorf("could not locate %q", p.Query)
						}
						people = append(people, Person{Name: p.Name, Lat: p.Lat, Lng: p.Lng, Count: p.Count})
					}
				}
				res, err := db.Solve(people, c)
				if err != nil {
					return nil, err
				}
				return compact(res), nil
			},
		},
		{
			Name:        "estimate_fare",
			Description: "Estimate the round-trip economy fare and stops between two airports (IATA codes).",
			Parameters:  obj(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"]}`),
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ From, To string }
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, err
				}
				from, to := strings.ToUpper(a.From), strings.ToUpper(a.To)
				if db.ByIATA[from] == nil || db.ByIATA[to] == nil {
					return nil, fmt.Errorf("unknown airport code")
				}
				stops, ok := db.Stops(from, to)
				if !ok {
					return map[string]any{"reachable": false}, nil
				}
				return map[string]any{
					"from": from, "to": to, "stops": stops, "km": roundTo(db.Distance(from, to), 1),
					"fare_usd_round_trip": roundTo(db.EstimateOneWay(from, to, stops)*2, 5),
					"hours_one_way":       roundTo(db.EstimateHours(from, to, stops), 0.1),
				}, nil
			},
		},
		{
			Name:        "search_airports",
			Description: "Find airports by city or country, or list the airports nearest a place.",
			Parameters:  obj(`{"type":"object","properties":{"city":{"type":"string"},"country":{"type":"string","description":"ISO code"},"near":{"type":"string","description":"A place name to search near"}}}`),
			Run: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ City, Country, Near string }
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, err
				}
				if a.Near != "" {
					p := geo.Resolve(ctx, []Place{{Query: a.Near}})[0]
					if !p.Resolved {
						return nil, fmt.Errorf("could not locate %q", a.Near)
					}
					var out []map[string]any
					for _, n := range db.Nearest(p.Lat, p.Lng, 5, 300) {
						out = append(out, map[string]any{"iata": n.Airport.IATA, "name": n.Airport.Name, "city": n.Airport.City, "km": roundTo(n.Km, 1)})
					}
					return out, nil
				}
				var hits []*Airport
				for i := range db.Airports {
					ap := &db.Airports[i]
					if (a.City == "" || strings.Contains(lower(ap.City), lower(a.City))) && (a.Country == "" || strings.EqualFold(ap.Country, a.Country)) {
						hits = append(hits, ap)
					}
				}
				sort.Slice(hits, func(i, j int) bool { return hits[i].Routes > hits[j].Routes })
				var out []map[string]any
				for _, ap := range hits[:min(len(hits), 12)] {
					out = append(out, map[string]any{"iata": ap.IATA, "name": ap.Name, "city": ap.City, "country": ap.Country, "routes": ap.Routes})
				}
				return out, nil
			},
		},
	}
}

func merge(dst *Constraints, src Constraints) {
	if src.MaxFare > 0 {
		dst.MaxFare = src.MaxFare
	}
	if src.MaxStops != nil {
		dst.MaxStops = src.MaxStops
	}
	if src.Objective != "" {
		dst.Objective = src.Objective
	}
	if src.HourlyValue > 0 {
		dst.HourlyValue = src.HourlyValue
	}
	if src.MaxHours > 0 {
		dst.MaxHours = src.MaxHours
	}
	if len(src.Countries) > 0 {
		dst.Countries = src.Countries
	}
	if len(src.Include) > 0 {
		dst.Include = src.Include
	}
	if len(src.Exclude) > 0 {
		dst.Exclude = src.Exclude
	}
	if src.TopN > 0 {
		dst.TopN = src.TopN
	}
}

func compact(r *Result) map[string]any {
	var opts []map[string]any
	for _, o := range r.Options {
		var legs []map[string]any
		for _, l := range o.Legs {
			legs = append(legs, map[string]any{"person": l.Person, "from": l.Origin, "fare_usd": l.FareUSD, "stops": l.Stops, "hours": l.Hours})
		}
		opts = append(opts, map[string]any{
			"iata": o.Airport.IATA, "city": o.Airport.City, "country": o.Airport.Country,
			"total_usd": o.TotalUSD, "mean_usd": o.MeanUSD, "max_usd": o.MaxUSD, "max_hours": o.MaxHours, "legs": legs,
		})
	}
	return map[string]any{"options": opts, "considered": r.Considered, "dropped": r.Dropped, "note": r.Note}
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}
