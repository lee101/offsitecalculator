package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

type airport struct {
	IATA      string  `json:"i"`
	Name      string  `json:"n"`
	City      string  `json:"c"`
	Country   string  `json:"k"`
	Continent string  `json:"w"`
	Lat       float64 `json:"y"`
	Lng       float64 `json:"x"`
	Large     bool    `json:"l,omitempty"`
	Routes    int     `json:"r"`
}

func main() {
	raw, out := "data/raw", "offsite/data"
	routes := loadRoutes(raw + "/routes.dat")
	f, err := os.Open(raw + "/airports.csv")
	check(err)
	rows, err := csv.NewReader(f).ReadAll()
	check(err)
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	type pt struct{ lat, lng float64 }
	coords := map[string]pt{}
	for _, r := range rows[1:] {
		if iata := r[col["iata_code"]]; iata != "" {
			lat, _ := strconv.ParseFloat(r[col["latitude_deg"]], 64)
			lng, _ := strconv.ParseFloat(r[col["longitude_deg"]], 64)
			coords[iata] = pt{lat, lng}
		}
	}
	if of, err := os.Open(raw + "/openflights_airports.dat"); err == nil {
		ofRows, _ := csv.NewReader(of).ReadAll()
		for _, r := range ofRows {
			if len(r) > 7 && len(r[4]) == 3 {
				if _, ok := coords[r[4]]; !ok {
					lat, _ := strconv.ParseFloat(r[6], 64)
					lng, _ := strconv.ParseFloat(r[7], 64)
					coords[r[4]] = pt{lat, lng}
				}
			}
		}
	}
	// Airports opened or renamed after the 2014 route snapshot (BER, WMI...) inherit
	// the routes of a donor airport within 40 km, e.g. Schoenefeld for Berlin.
	for _, r := range rows[1:] {
		iata, typ := r[col["iata_code"]], r[col["type"]]
		if iata == "" || r[col["scheduled_service"]] != "yes" || typ != "large_airport" || len(routes[iata]) >= 5 {
			continue
		}
		me := coords[iata]
		for donor, dset := range routes {
			if donor == iata || len(dset) < 5 {
				continue
			}
			if c := coords[donor]; haversine(me.lat, me.lng, c.lat, c.lng) < 25 {
				if routes[iata] == nil {
					routes[iata] = map[string]bool{}
				}
				for d := range dset {
					routes[iata][d] = true
					if routes[d] != nil {
						routes[d][iata] = true
					}
				}
			}
		}
	}
	var airports []airport
	keep := map[string]bool{}
	for _, r := range rows[1:] {
		iata := r[col["iata_code"]]
		typ := r[col["type"]]
		if iata == "" || r[col["scheduled_service"]] != "yes" || (typ != "large_airport" && typ != "medium_airport") {
			continue
		}
		if len(routes[iata]) == 0 {
			continue
		}
		lat, _ := strconv.ParseFloat(r[col["latitude_deg"]], 64)
		lng, _ := strconv.ParseFloat(r[col["longitude_deg"]], 64)
		city := r[col["municipality"]]
		if city == "" {
			city = r[col["name"]]
		}
		airports = append(airports, airport{
			IATA: iata, Name: r[col["name"]], City: city, Country: r[col["iso_country"]],
			Continent: r[col["continent"]], Lat: round(lat), Lng: round(lng),
			Large: typ == "large_airport", Routes: len(routes[iata]),
		})
		keep[iata] = true
	}
	sort.Slice(airports, func(i, j int) bool { return airports[i].IATA < airports[j].IATA })
	adj := map[string][]string{}
	for a, dsts := range routes {
		if !keep[a] {
			continue
		}
		var list []string
		for d := range dsts {
			if keep[d] {
				list = append(list, d)
			}
		}
		sort.Strings(list)
		if len(list) > 0 {
			adj[a] = list
		}
	}
	check(os.MkdirAll(out, 0o755))
	write(out+"/airports.json", airports)
	write(out+"/routes.json", adj)
	fmt.Printf("airports=%d origins_with_routes=%d\n", len(airports), len(adj))
}

func loadRoutes(path string) map[string]map[string]bool {
	f, err := os.Open(path)
	check(err)
	rows, err := csv.NewReader(f).ReadAll()
	check(err)
	out := map[string]map[string]bool{}
	add := func(a, b string) {
		if out[a] == nil {
			out[a] = map[string]bool{}
		}
		out[a][b] = true
	}
	for _, r := range rows {
		if len(r) < 8 || len(r[2]) != 3 || len(r[4]) != 3 || strings.Contains(r[2]+r[4], `\N`) {
			continue
		}
		add(r[2], r[4])
		add(r[4], r[2])
	}
	return out
}

func round(v float64) float64 { return float64(int(v*1000+sign(v)*0.5)) / 1000 }

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

func write(path string, v any) {
	b, err := json.Marshal(v)
	check(err)
	check(os.WriteFile(path, b, 0o644))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func haversine(lat1, lng1, lat2, lng2 float64) float64 {
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lng2-lng1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 12742 * math.Asin(math.Min(1, math.Sqrt(a)))
}
