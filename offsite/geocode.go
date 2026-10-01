package offsite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Place struct {
	Query    string  `json:"query"`
	Name     string  `json:"name"`
	Label    string  `json:"label,omitempty"`
	Lat      float64 `json:"lat"`
	Lng      float64 `json:"lng"`
	Count    int     `json:"count"`
	Resolved bool    `json:"resolved"`
	Source   string  `json:"source,omitempty"`
}

var (
	latLngRe = regexp.MustCompile(`^\s*(-?\d{1,2}(?:\.\d+)?)\s*[,;\s]\s*(-?\d{1,3}(?:\.\d+)?)\s*$`)
	countRe  = regexp.MustCompile(`(?i)^\s*(\d+)\s*x\s+|\s+x\s*(\d+)\s*$|\s*\((\d+)\)\s*$`)
	iataRe   = regexp.MustCompile(`^[A-Za-z]{3}$`)
)

var countryNames = map[string]string{
	"usa": "US", "united states": "US", "america": "US", "uk": "GB", "united kingdom": "GB", "england": "GB",
	"scotland": "GB", "australia": "AU", "new zealand": "NZ", "nz": "NZ", "canada": "CA", "germany": "DE",
	"france": "FR", "spain": "ES", "italy": "IT", "japan": "JP", "china": "CN", "india": "IN", "brazil": "BR",
	"mexico": "MX", "singapore": "SG", "netherlands": "NL", "ireland": "IE", "portugal": "PT", "sweden": "SE",
	"norway": "NO", "denmark": "DK", "finland": "FI", "poland": "PL", "switzerland": "CH", "austria": "AT",
	"belgium": "BE", "greece": "GR", "turkey": "TR", "uae": "AE", "south africa": "ZA", "south korea": "KR",
	"argentina": "AR", "chile": "CL", "colombia": "CO", "thailand": "TH", "indonesia": "ID", "philippines": "PH",
	"vietnam": "VN", "israel": "IL", "egypt": "EG", "kenya": "KE", "nigeria": "NG", "czechia": "CZ",
}

// ParseLines turns a pasted list into places. Accepted per line:
// "Alice: Auckland", "Bob - London, UK", "3x Sydney", "Berlin x2", "SFO", "37.77,-122.42".
func ParseLines(text string) []Place {
	var out []Place
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := Place{Count: 1}
		if m := countRe.FindStringSubmatch(line); m != nil {
			for _, g := range m[1:] {
				if n, err := strconv.Atoi(g); err == nil && n > 0 {
					p.Count = min(n, 500)
				}
			}
			line = strings.TrimSpace(countRe.ReplaceAllString(line, ""))
		}
		if i := strings.IndexAny(line, ":\t"); i > 0 && !latLngRe.MatchString(line) {
			p.Name, line = strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
		} else if i := strings.Index(line, " - "); i > 0 {
			p.Name, line = strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+3:])
		}
		p.Query = line
		if p.Name == "" {
			p.Name = line
		}
		out = append(out, p)
	}
	return out
}

// Gazetteer resolves names against the bundled airport cities. It is the
// no-key fallback; Geocoder layers Google on top.
func (db *DB) Gazetteer(q string) (Place, bool) {
	q = strings.TrimSpace(q)
	if m := latLngRe.FindStringSubmatch(q); m != nil {
		lat, _ := strconv.ParseFloat(m[1], 64)
		lng, _ := strconv.ParseFloat(m[2], 64)
		return Place{Query: q, Lat: lat, Lng: lng, Resolved: true, Source: "coordinates", Label: q}, true
	}
	if iataRe.MatchString(q) {
		if a := db.ByIATA[strings.ToUpper(q)]; a != nil {
			return Place{Query: q, Lat: a.Lat, Lng: a.Lng, Resolved: true, Source: "airport", Label: a.City + " (" + a.IATA + ")"}, true
		}
	}
	city, country := q, ""
	if i := strings.LastIndex(q, ","); i > 0 {
		city, country = strings.TrimSpace(q[:i]), strings.ToLower(strings.TrimSpace(q[i+1:]))
		if iso, ok := countryNames[country]; ok {
			country = strings.ToLower(iso)
		}
	}
	city = strings.ToLower(city)
	var best *Airport
	for i := range db.Airports {
		a := &db.Airports[i]
		if lower(a.City) != city && !strings.HasPrefix(lower(a.City), city+" ") {
			continue
		}
		if country != "" && strings.ToLower(a.Country) != country && !strings.EqualFold(a.Name, country) {
			continue
		}
		if best == nil || a.Routes > best.Routes {
			best = a
		}
	}
	if best == nil {
		return Place{Query: q}, false
	}
	return Place{Query: q, Lat: best.Lat, Lng: best.Lng, Resolved: true, Source: "airport-city", Label: best.City + ", " + best.Country}, true
}

type Geocoder struct {
	DB     *DB
	APIKey string
	Client *http.Client
}

func (g *Geocoder) Resolve(ctx context.Context, places []Place) []Place {
	db := g.DB
	if db == nil {
		db = Default()
	}
	for i := range places {
		p := &places[i]
		if g.APIKey != "" {
			if lat, lng, label, err := g.google(ctx, p.Query); err == nil {
				p.Lat, p.Lng, p.Label, p.Resolved, p.Source = lat, lng, label, true, "google"
				continue
			}
		}
		if hit, ok := db.Gazetteer(p.Query); ok {
			p.Lat, p.Lng, p.Label, p.Resolved, p.Source = hit.Lat, hit.Lng, hit.Label, true, hit.Source
		}
	}
	return places
}

func (g *Geocoder) google(ctx context.Context, q string) (float64, float64, string, error) {
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	u := "https://maps.googleapis.com/maps/api/geocode/json?address=" + url.QueryEscape(q) + "&key=" + url.QueryEscape(g.APIKey)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, "", err
	}
	defer resp.Body.Close()
	var out struct {
		Status  string `json:"status"`
		Results []struct {
			Formatted string `json:"formatted_address"`
			Geometry  struct {
				Location struct{ Lat, Lng float64 }
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, 0, "", err
	}
	if out.Status != "OK" || len(out.Results) == 0 {
		return 0, 0, "", fmt.Errorf("geocode %s", out.Status)
	}
	r := out.Results[0]
	return r.Geometry.Location.Lat, r.Geometry.Location.Lng, r.Formatted, nil
}
