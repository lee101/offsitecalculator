package offsite

import (
	_ "embed"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
)

//go:embed data/airports.json
var airportsJSON []byte

//go:embed data/routes.json
var routesJSON []byte

type Airport struct {
	IATA      string  `json:"iata"`
	Name      string  `json:"name"`
	City      string  `json:"city"`
	Country   string  `json:"country"`
	Continent string  `json:"continent"`
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Large     bool    `json:"large,omitempty"`
	Routes    int     `json:"routes"`
}

type DB struct {
	Airports []Airport
	ByIATA   map[string]*Airport
	adj      map[string][]string
	direct   map[string]map[string]bool

	mu  sync.Mutex
	bfs map[string]map[string]uint8
}

var (
	defaultDB   *DB
	defaultOnce sync.Once
)

func Default() *DB {
	defaultOnce.Do(func() {
		db, err := Load(airportsJSON, routesJSON)
		if err != nil {
			panic(err)
		}
		defaultDB = db
	})
	return defaultDB
}

func Load(airports, routes []byte) (*DB, error) {
	var rawKeyed []map[string]any
	if err := json.Unmarshal(airports, &rawKeyed); err != nil {
		return nil, err
	}
	db := &DB{ByIATA: map[string]*Airport{}, adj: map[string][]string{}, direct: map[string]map[string]bool{}, bfs: map[string]map[string]uint8{}}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	num := func(m map[string]any, k string) float64 { f, _ := m[k].(float64); return f }
	for _, m := range rawKeyed {
		large, _ := m["l"].(bool)
		db.Airports = append(db.Airports, Airport{
			IATA: str(m, "i"), Name: str(m, "n"), City: str(m, "c"), Country: str(m, "k"), Continent: str(m, "w"),
			Lat: num(m, "y"), Lng: num(m, "x"), Large: large, Routes: int(num(m, "r")),
		})
	}
	for i := range db.Airports {
		db.ByIATA[db.Airports[i].IATA] = &db.Airports[i]
	}
	if err := json.Unmarshal(routes, &db.adj); err != nil {
		return nil, err
	}
	for a, list := range db.adj {
		db.direct[a] = map[string]bool{}
		for _, b := range list {
			db.direct[a][b] = true
		}
	}
	return db, nil
}

func (db *DB) HasDirect(a, b string) bool { return db.direct[a][b] }

func Haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lng2-lng1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

func (db *DB) Distance(a, b string) float64 {
	x, y := db.ByIATA[a], db.ByIATA[b]
	if x == nil || y == nil {
		return 0
	}
	return Haversine(x.Lat, x.Lng, y.Lat, y.Lng)
}

// Origins returns the airports a traveller could realistically use: real
// airline airports (not strips) within a few hours' drive, nearest first.
func (db *DB) Origins(lat, lng float64) []Near {
	var out []Near
	for i := range db.Airports {
		a := &db.Airports[i]
		if a.Routes < 25 {
			continue
		}
		if d := Haversine(lat, lng, a.Lat, a.Lng); d <= 250 {
			out = append(out, Near{a, d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Km < out[j].Km })
	if len(out) > 6 {
		out = out[:6]
	}
	if len(out) == 0 {
		return db.Nearest(lat, lng, 1, 0)
	}
	return out
}

type Near struct {
	Airport *Airport
	Km      float64
}

func (db *DB) Nearest(lat, lng float64, k int, maxKm float64) []Near {
	var out []Near
	for i := range db.Airports {
		a := &db.Airports[i]
		d := Haversine(lat, lng, a.Lat, a.Lng)
		if d <= maxKm {
			out = append(out, Near{a, d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := out[i].Km-float64(min(out[i].Airport.Routes, 150))*1.2, out[j].Km-float64(min(out[j].Airport.Routes, 150))*1.2
		return si < sj
	})
	if len(out) > k {
		out = out[:k]
	}
	if len(out) == 0 {
		best := Near{Km: math.MaxFloat64}
		for i := range db.Airports {
			a := &db.Airports[i]
			if d := Haversine(lat, lng, a.Lat, a.Lng); d < best.Km {
				best = Near{a, d}
			}
		}
		if best.Airport != nil {
			out = append(out, best)
		}
	}
	return out
}

const maxStopsCap = 2

func (db *DB) stopsFrom(origin string) map[string]uint8 {
	db.mu.Lock()
	defer db.mu.Unlock()
	if m, ok := db.bfs[origin]; ok {
		return m
	}
	dist := map[string]uint8{origin: 0}
	frontier := []string{origin}
	for depth := uint8(0); depth <= maxStopsCap && len(frontier) > 0; depth++ {
		var next []string
		for _, a := range frontier {
			for _, b := range db.adj[a] {
				if _, seen := dist[b]; !seen {
					dist[b] = depth
					next = append(next, b)
				}
			}
		}
		frontier = next
	}
	db.bfs[origin] = dist
	return dist
}

func (db *DB) Stops(origin, dest string) (int, bool) {
	if origin == dest {
		return 0, true
	}
	s, ok := db.stopsFrom(origin)[dest]
	return int(s), ok
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
