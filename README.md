# Offsite Calculator

Drop your team on a map, get the cities where you could all meet for the least flight money. Click the map or paste a list of locations, cap the fare per person, allow or forbid connections, and rank by cheapest total, fairest, or a mix. Live at <https://offsitecalculator.app.nz>.

It is also a worked example of a small agent in Go on [app.nz](https://app.nz): the calculator is free and keyless, and an optional **AI planner** runs on the signed-in person's own app.nz credits, pinned to one approved model.

```
browser  --/api/solve-->  Go solver (airports + routes + fare model)       free, no login
browser  --/api/agent-->  Go agent loop --Bearer--> app.nz/v1/chat/completions   user's credits
              ^                  tools: find_offsite_destinations, estimate_fare, search_airports
              '-- "Sign in with app.nz": OAuth + PKCE, scopes profile credits:read models:chat
```

## Run it

```sh
cp .env.example .env     # set SESSION_SECRET; add Google Maps keys if you have them
make run                 # http://localhost:8080
make test
```

Without Google keys the map is Leaflet/OpenStreetMap and pasted lists resolve against the bundled airport-city gazetteer. Set `GOOGLE_MAPS_API_KEY` (Geocoding API, server-side) and `GOOGLE_MAPS_BROWSER_KEY` (Maps JavaScript API, referrer-restricted) to switch to Google Maps and street-level geocoding.

## Deploy

```sh
curl https://app.nz/cli.sh | sh && app auth login
make deploy              # static site on the CDN + Go backend at <slug>.app.nz
```

`appnz.yaml` declares a hybrid app: `web/` is published as static files, `/api/*` and writes are proxied to the Go binary. The same binary also serves `web/` itself (`go:embed`), so a `Dockerfile` or any container host works too.

## Build an agent on app.nz (the `appnz` package)

`appnz/` has no dependencies and no server-side state, so it suits scale-to-zero hosting. The whole pattern:

```go
client := appnz.New("your-client-id")                       // public PKCE client from app.nz/developers

// 1. Ask for exactly one model. app.nz shows it on the consent screen and refuses any other.
auth := client.AuthStart(redirectURI, []string{"profile", "credits:read", "models:chat"}, []string{"gpt-5.5"})
// store auth.State + auth.Verifier in a sealed cookie, redirect to auth.URL

// 2. On callback, trade the code for a token and keep it in a sealed HttpOnly cookie.
tok, _ := client.Exchange(ctx, redirectURI, code, verifier)
sealed, _ := sealer.Seal(tok)                               // AES-256-GCM, appnz.NewSealer(secret)

// 3. Give the model tools and run the loop on the person's credits.
agent := &appnz.Agent{Client: client, Model: "gpt-5.5", System: prompt, Tools: tools}
res, err := agent.Run(ctx, tok.AccessToken, history)
if errors.Is(err, appnz.ErrInsufficientCredits) { /* link to client.TopUpURL() */ }
```

A tool is a name, a JSON Schema and a Go function (`appnz.Tool`). See `offsite/tools.go` for three, and `server/auth.go` + `server/agent.go` for the complete login and agent endpoints.

**The `models:chat` scope.** Unlike `api`, it cannot call anything but `POST /v1/chat/completions`, only for the models named in the authorize request (`models=gpt-5.5`), with no rerouting or fallback to another model. Spend is billed to the person's credits; their balance is readable with `credits:read`, and they top up at app.nz/billing. Your app never handles payment details or an API key.

## How the numbers are made

- The pre-crawl (`scripts/crawl.sh`, raw files in `data/raw/`, not committed): [OurAirports](https://ourairports.com/data/) (public domain) for airports and [OpenFlights](https://openflights.org/data.html) (ODbL) for the 2014 route graph. `make data` rebuilds `offsite/data/*.json`; airports opened since 2014 inherit routes from a nearby donor airport.
- Stops come from a breadth-first search over the direct-route graph (0 to 2 connections).
- Fares are a **rough model**, not quotes: a distance curve with regional adjustments, doubled for a round trip, plus ground transport to a farther airport. `DB.EstimateOneWay` in `offsite/fares.go` is the one function to replace with a live fare API.
- Options are ranked per metro area so the top five are not five airports of one city.

## Layout

```
appnz/      app.nz SDK: OAuth+PKCE, sealed sessions, chat, agent loop
offsite/    airports, routes, fare model, solver, geocoding, agent tools
server/     net/http handlers: solve, geocode, auth, agent
web/        no-build front end (Google Maps or Leaflet)
cmd/        offsite (server), builddata (pre-crawl)
```

MIT licensed.
